set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
go build -trimpath -ldflags="-s -w" -o frps_%GOOS%_%GOARCH%.exe ./cmd/frps
upx -9 frps_%GOOS%_%GOARCH%.exe

go build -trimpath -ldflags="-s -w" -o frpc_%GOOS%_%GOARCH%.exe ./cmd/frpc
upx -9 frpc_%GOOS%_%GOARCH%.exe
pause
