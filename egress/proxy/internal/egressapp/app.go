package egressapp

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"time"

	"daxiang-vpn/egress/proxy/internal/egressconfig"
	"daxiang-vpn/egress/proxy/internal/egressproxy"
	"daxiang-vpn/shared/paths"
	"daxiang-vpn/shared/proxy"
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
	fmt.Println(`dxegress-proxy

用法：
  dxegress-proxy validate --config <配置文件>
  dxegress-proxy render --config <配置文件> [--workdir <目录>]
  dxegress-proxy run --config <配置文件> [--workdir <目录>]

说明：
  预留给 Mac/PC 出口节点的 sing-box 代理封装。
  Android 生产出口请使用 egress/reverse 的 dxreverse 反向数据面。`)
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
	if root := os.Getenv("DXEGRESS_PROXY_HOME"); root != "" {
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
	cfg, err := egressconfig.Load(configPath)
	if err != nil {
		return err
	}
	if err := render(configPath, workdir); err != nil {
		return err
	}
	if !cfg.WireGuard.ExternalMode() && cfg.WireGuard.SystemTun() {
		go ensureWGRouting(cfg)
	}
	fmt.Println("启动出口守护进程（前台）")
	return proxy.RunEngine(paths.FromRoot(workdir))
}

// ensureWGRouting 在系统 WG 模式下，等待 wg0 接口就绪后添加一条策略路由规则，
// 让发往 WG 子网（含 Hub 内网地址）的流量走 main 路由表，从而命中 wg0 的连接路由。
// 部分系统使用基于 fwmark 的策略路由，默认不查 main 表，导致回 Hub 的 SYN-ACK
// 误走默认路由而丢失。此规则修复该问题。
func ensureWGRouting(cfg egressconfig.Config) {
	subnet, err := cfg.WireGuard.SubnetCIDR()
	if err != nil {
		fmt.Printf("警告：无法解析 WG 子网，跳过路由设置：%v\n", err)
		return
	}
	for i := 0; i < 30; i++ {
		if wgInterfaceReady("wg0") {
			break
		}
		time.Sleep(time.Second)
	}
	// 幂等：先删除同优先级的旧规则再添加，忽略删除错误。
	_ = exec.Command("ip", "rule", "del", "to", subnet, "lookup", "main", "pref", "9999").Run()
	if out, err := exec.Command("ip", "rule", "add", "to", subnet, "lookup", "main", "pref", "9999").CombinedOutput(); err != nil {
		fmt.Printf("警告：添加 WG 策略路由失败（可能缺少 root 或 ip 工具）：%v %s\n", err, string(out))
		return
	}
	fmt.Printf("已添加 WG 策略路由：to %s lookup main pref 9999\n", subnet)
}

func wgInterfaceReady(name string) bool {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return false
	}
	addrs, err := ifi.Addrs()
	return err == nil && len(addrs) > 0
}
