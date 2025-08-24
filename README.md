# texpect (tmux expect)

texpectは、tmuxウィンドウをexpectやteratermマクロのように自動操作するためのツールです。
ユーザがLuaスクリプトを記述して自動化を実現します。

texpectで解決できることは下記
- Lua言語機能がそのまま使用できるためexpectやteratermマクロの特殊な文法を使わずに安易にスクリプトの記述が可能となります。
- tmuxを操作するためのAPIを提供します。実行時に複数のセッション(tmuxのウィンドウを生成)するので、複数サーバに対してコマンド発行や指定文字列の待機が可能となります。

自動化を実現するためにAPIは下記
- tmuxウィンドウの生成・管理（spawn）
- 任意のコマンド送信（send）
- 指定した文字列が出力されるまで待機（expect, expectAny）
- スクリプト内でのスリープ（sleep）
- セッションの終了（exit）

## API詳細

- spawn(): tmuxのウィンドウを開始する。
  - 引数1(str): tmuxウィンドウ名
  - 引数2(str): 実行するシェルやssh
- send(): 文字列を指定したtmuxウィンドウに送信する。
  - 引数1(str): tmuxウィンドウ名
  - 引数2(str): 送信する文字列
- expect(): 文字列が出力されるまで待機する。**全てのtmuxウィンドウを対象とする**
  - 引数1(str): 待機する文字列
  - 引数2(int, 省略可能): タイムアウト秒(math.MaxInt)
  - 戻り値: 0 なら成功、-1ならタイムアウト。
- expectAny(): いずれかの文字列が出力されるまで待機する。**全てのtmuxセッションを対象とする**
  - 引数1(table): 待機する文字列の配列
  - 引数2(int, 省略可能): タイムアウト秒(math.MaxInt)
  - 戻り値: 0以上なら一致した文字列のインデックス、1ならタイムアウト。 
- sleep(): 指定時間スリープする。
  - 引数1(int): スリープ秒
- exit(): tmuxセッションを終了する。

Example1
```lua
local server = "serv1"  -- tmuxウィンドウ名 
spawn("bash", server)   -- tmuxセッションを開始
send(server, "date")    -- dateコマンドを実行
expect("2025")          -- 2025年の日付が出力されるまで待機
                        -- exit()を呼ばないのでtmuxセッションは終了しない                   
```

Example2
```lua
local server1 = "serv1" -- tmuxウィンドウ名
local server2 = "serv2" -- tmuxウィンドウ名

spawn(server1, "ssh root@192.168.0.1")
spawn(server2, "ssh root@192.168.0.2")

send(server1, "backup1.sh &")  -- バックアップを非同期で実行
send(server2, "backup2.sh &")  -- バックアップを非同期で実行

local backup_done = {"backup1 completed", "backup2 completed"}
ret1 = expectAny(backup_done)  -- "backup1 completed"が見つかった場合はret1=0 (index=0)
ret2 = expectAny(backup_done)  -- "backup2 completed"が見つかった場合はret2=1 (index=1)

exit()
```

## Usage

```bash
$ texpect -h
  -f string
        Path to Lua script file
  -t    Open tmux choose-tree
```

Example
```bash
texpect -f script.lua
```

## Internal

- Lua
  - GopherLuaを使用してLuaスクリプトを実行。
- API
  - spawn()は、`tmux new-window`を使用して新しいtmuxウィンドウを生成し、セッションを開始する。
  - send()は、`tmux send-keys`を使用してwindowにコマンドを送信します。
  - expect()は、`tmux pipe-pane`を使用しファイルに出力し、inotifyで追記があるか監視する。
    - goroutineを使用し、複数ファイルを同時に監視し、チャネルを使用して複数のファイルを行の更新をシリアライズ化する。
  - exit()は、`tmux kill-session`を使用してセッションを終了する。

NOTE: `tmux pipe-pane`を使用するため必ずログファイルが出力される。


