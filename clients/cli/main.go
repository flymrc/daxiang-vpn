package main

import (
	"errors"
	"fmt"
	"os"

	"zongheng-vpn/clients/cli/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:]); err != nil {
		// JSON 命令失败时已把 {"ok":false,...} 打到 stdout，这里只需退出非 0，
		// 不再向 stderr 重复打印，避免污染 GUI 解析的输出。
		if !errors.Is(err, app.ErrSilent) {
			fmt.Fprintf(os.Stderr, "错误：%v\n", err)
		}
		os.Exit(1)
	}
}
