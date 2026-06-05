package egressapp

import (
	"errors"
	"fmt"
	"os"

	"daxiang-vpn/frontend/dxvpn/internal/egressconfig"
	"daxiang-vpn/frontend/dxvpn/internal/egressproxy"
	"daxiang-vpn/frontend/dxvpn/internal/paths"
	"daxiang-vpn/frontend/dxvpn/internal/proxy"
)

func Run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "validate":
		cfgPath, _, err := parseCommonArgs(args[1:])
		if err != nil {
			return err
		}
		cfg, err := egressconfig.Load(cfgPath)
		if err != nil {
			return err
		}
		addr, err := cfg.ProxyAddr()
		if err != nil {
			return err
		}
		fmt.Println("配置有效")
		fmt.Printf("节点：%s\n", cfg.Node.Name)
		fmt.Printf("代理监听：%s\n", addr)
		return nil
	case "render":
		cfgPath, workdir, err := parseCommonArgs(args[1:])
		if err != nil {
			return err
		}
		return render(cfgPath, workdir)
	case "run":
		cfgPath, workdir, err := parseCommonArgs(args[1:])
		if err != nil {
			return err
		}
		return run(cfgPath, workdir)
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("未知命令：%s", args[0])
	}
}

func printUsage() {
	fmt.Println(`dxandroid-egress

用法：
  dxandroid-egress validate --config <配置文件>
  dxandroid-egress render --config <配置文件> [--workdir <目录>]
  dxandroid-egress run --config <配置文件> [--workdir <目录>]

说明：
  第一版定位为 Android root 出口节点守护进程。
  先以 shell / adb 启动，后续再补 Android App 外壳与保活。`)
}

func parseCommonArgs(args []string) (configPath string, workdir string, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--config":
			if i+1 >= len(args) {
				return "", "", errors.New("--config 需要一个文件路径")
			}
			configPath = args[i+1]
			i++
		case "--workdir":
			if i+1 >= len(args) {
				return "", "", errors.New("--workdir 需要一个目录路径")
			}
			workdir = args[i+1]
			i++
		default:
			return "", "", fmt.Errorf("未知参数：%s", args[i])
		}
	}
	if configPath == "" {
		return "", "", errors.New("缺少 --config")
	}
	if workdir == "" {
		workdir = filepathOrDefault(configPath)
	}
	return configPath, workdir, nil
}

func filepathOrDefault(configPath string) string {
	if root := os.Getenv("DXANDROID_HOME"); root != "" {
		return root
	}
	return configPath + ".workdir"
}

func render(configPath string, workdir string) error {
	cfg, err := egressconfig.Load(configPath)
	if err != nil {
		return err
	}
	ctx := paths.FromRoot(workdir)
	if err := ctx.EnsureDirs(); err != nil {
		return err
	}
	if err := egressproxy.WriteConfig(ctx, cfg); err != nil {
		return err
	}
	addr, err := cfg.ProxyAddr()
	if err != nil {
		return err
	}
	fmt.Println("运行配置已生成")
	fmt.Printf("工作目录：%s\n", workdir)
	fmt.Printf("代理监听：%s\n", addr)
	return nil
}

func run(configPath string, workdir string) error {
	if err := render(configPath, workdir); err != nil {
		return err
	}
	fmt.Println("启动出口守护进程（前台）")
	return proxy.RunEngine(paths.FromRoot(workdir))
}
