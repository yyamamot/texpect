.PHONY: all
all:
	go mod tidy
	go build -ldflags "-s -w" -trimpath -o texpect main.go
