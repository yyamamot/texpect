package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/yuin/gopher-lua"
)

var (
	windowMap  = make(map[string]*Window)
	ansiRegexp = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
)

func removeANSIEscapeSequences(s string) string {
	return ansiRegexp.ReplaceAllString(s, "")
}

type Window struct {
	name      string
	command   string
	createdAt time.Time
	watcher   *LineWatcher
}

func NewWindow(name, command string, watcher *LineWatcher) *Window {
	return &Window{
		name:      name,
		command:   command,
		createdAt: time.Now(),
		watcher:   watcher,
	}
}

func (w Window) GetName() string {
	return w.name
}

func (w Window) LogPath() string {
	return fmt.Sprintf("%s-%s.log", w.createdAt.Format("20060102"), w.name)
}

func (w Window) Start() error {
	out, err := exec.Command("tmux", "list-windows", "-F", "#{window_name}").Output()
	if err != nil {
		return fmt.Errorf("failed to list tmux windows: %v", err)
	}

	windowNames := strings.Split(string(out), "\n")
	for _, name := range windowNames {
		if name == w.name {
			return fmt.Errorf("window '%s' already exists", w.name)
		}
	}

	if err := exec.Command("tmux", "new-window", "-d", "-n", w.name, w.command).Run(); err != nil {
		return fmt.Errorf("failed to create tmux window: %v", err)
	}
	if err := exec.Command("tmux", "pipe-pane", "-t", w.name, fmt.Sprintf("cat >> %s", w.LogPath())).Run(); err != nil {
		return fmt.Errorf("failed to set tmux pipe-pane: %v", err)
	}
	_, _ = os.Create(w.LogPath())

	w.watcher.AddFilePath(w.LogPath())
	return nil
}

func (w Window) SendCommand(command string) {
	_ = exec.Command("tmux", "send-keys", "-t", w.name, command, "C-m").Run()
}

type LineWatcher struct {
	filePathCh chan string
	recvLineCh chan string
	ctx        context.Context
}

func NewLineWatcher(ctx context.Context) *LineWatcher {
	return &LineWatcher{
		filePathCh: make(chan string),
		recvLineCh: make(chan string),
		ctx:        ctx,
	}
}

func (l *LineWatcher) AddFilePath(filePath string) {
	l.filePathCh <- filePath
}

func (l *LineWatcher) watchLatestLine() error {
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for {
		select {
		case path, ok := <-l.filePathCh:
			if !ok {
				wg.Wait()
				return nil
			}
			wg.Add(1)
			go func(p string) {
				defer wg.Done()
				_ = l.watchFileLines(ctx, p)
			}(path)
		case <-ctx.Done():
			wg.Wait()
			return nil
		}
	}
}

func (l *LineWatcher) watchFileLines(ctx context.Context, filePath string) error {

	watcher, _ := fsnotify.NewWatcher()
	defer watcher.Close()
	watcher.Add(filePath)

	file, _ := os.Open(filePath)
	defer file.Close()
	_, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}

	reader := bufio.NewReader(file)
	var line strings.Builder
	for {
		select {
		case event := <-watcher.Events:
			if event.Op&fsnotify.Write == fsnotify.Write {
				for {
					b, err := reader.ReadByte()
					if err != nil {
						break
					}
					line.WriteByte(b)

					if b == '\n' {
						l.recvLineCh <- removeANSIEscapeSequences(line.String())
						line.Reset()
					}
				}
			}
		case err := <-watcher.Errors:
			return err
		case <-ctx.Done():
			return nil
		}
	}
}

func (l *LineWatcher) GetLineCh() <-chan string {
	return l.recvLineCh
}

func (l *LineWatcher) Watch() {
	go func() {
		if err := l.watchLatestLine(); err != nil {
			fmt.Println("Error watching files:", err)
			return
		}
	}()
}

func main() {
	var chooseTree bool
	var scriptFile string
	flag.StringVar(&scriptFile, "f", "", "Path to Lua script file")
	flag.BoolVar(&chooseTree, "t", false, "Open tmux choose-tree")
	flag.Parse()

	if scriptFile == "" {
		fmt.Println("Usage: texpect -f script.lua")
		os.Exit(1)
	}

	scriptData, err := os.ReadFile(scriptFile)
	if err != nil {
		fmt.Printf("Failed to read script file: %v\n", err)
		os.Exit(1)
	}

	if chooseTree {
		exec.Command("tmux", "choose-tree").Run()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lineWatcher := NewLineWatcher(ctx)
	lineWatcher.Watch()

	L := lua.NewState()
	defer L.Close()

	registerAPI(L, lineWatcher)

	if err := L.DoString(string(scriptData)); err != nil {
		fmt.Printf("Lua script execution error: %v\n", err)
	}
}

func registerAPI(L *lua.LState, watcher *LineWatcher) {
	//-------------------------------------------------------------------------
	// spawn
	//-------------------------------------------------------------------------
	L.SetGlobal("spawn", L.NewFunction(func(L *lua.LState) int {
		windowName := L.CheckString(1)
		command := L.CheckString(2)

		win := NewWindow(windowName, command, watcher)
		if err := win.Start(); err != nil {
			L.RaiseError("Failed to create window: %v", err)
			return 0
		}
		windowMap[windowName] = win

		return 0
	}))

	//-------------------------------------------------------------------------
	// send
	//-------------------------------------------------------------------------
	L.SetGlobal("send", L.NewFunction(func(L *lua.LState) int {
		windowName := L.CheckString(1)
		command := L.CheckString(2)
		fmt.Printf("send('%s', '%s')\n", windowName, command)

		win := windowMap[windowName]
		win.SendCommand(command)

		return 0
	}))

	//-------------------------------------------------------------------------
	// expect
	//-------------------------------------------------------------------------
	L.SetGlobal("expect", L.NewFunction(func(L *lua.LState) int {
		expectedText := L.CheckString(1)
		timeout := L.OptInt(2, math.MaxInt)
		if timeout == math.MaxInt {
			fmt.Printf("expect('%s')\n", expectedText)
		} else {
			fmt.Printf("expect('%s', '%d')\n", expectedText, timeout)
		}

		timeoutCh := time.After(time.Duration(timeout) * time.Second)

		for {
			select {
			case line := <-watcher.GetLineCh():
				if strings.Contains(line, expectedText) {
					L.Push(lua.LNumber(0))
					return 1
				}
			case <-timeoutCh:
				L.Push(lua.LNumber(-1))
				return 1
			}
		}
	}))

	//-------------------------------------------------------------------------
	// expectAny
	//-------------------------------------------------------------------------
	L.SetGlobal("expectAny", L.NewFunction(func(L *lua.LState) int {
		tbl := L.CheckTable(1)
		timeout := L.OptInt(2, math.MaxInt)

		expectedTexts := []string{}
		tbl.ForEach(func(_, v lua.LValue) {
			expectedTexts = append(expectedTexts, v.String())
		})

		timeoutCh := time.After(time.Duration(timeout) * time.Second)

		for {
			select {
			case line := <-watcher.GetLineCh():
				for index, expectedText := range expectedTexts {
					if strings.Contains(line, expectedText) {
						L.Push(lua.LNumber(index))
						return 1
					}
				}
			case <-timeoutCh:
				L.Push(lua.LNumber(-1))
				return 1
			}
		}
	}))

	//-------------------------------------------------------------------------
	// sleep
	//-------------------------------------------------------------------------
	L.SetGlobal("sleep", L.NewFunction(func(L *lua.LState) int {
		seconds := L.CheckInt(1)
		time.Sleep(time.Duration(seconds) * time.Second)
		return 0
	}))

	//-------------------------------------------------------------------------
	// exit
	//-------------------------------------------------------------------------
	L.SetGlobal("exit", L.NewFunction(func(L *lua.LState) int {
		exec.Command("tmux", "kill-session").Run()
		return 0
	}))
}

func example() {
	//exec.Command("tmux", "new-session", "-d", "-s", "texpect", "-n", "control").Run()
	exec.Command("tmux", "choose-tree").Run()
	lineWatcher := NewLineWatcher(context.Background())
	lineWatcher.Watch()

	win1 := NewWindow("script1", "./script1.sh", lineWatcher)
	win1.Start()
	time.Sleep(3 * time.Second) // Wait for the first window to start
	win2 := NewWindow("script2", "./script2.sh", lineWatcher)
	win2.Start()

	win1.SendCommand("echo 'Hello from script1'")
	win2.SendCommand("echo 'Hello from script2'")

	for line := range lineWatcher.GetLineCh() {
		fmt.Print(line)
	}

	time.Sleep(100 * time.Second) // Keep the main function running to allow watching
}
