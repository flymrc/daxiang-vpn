package app

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"daxiang-vpn/frontend/dxvpn/internal/bootstrap"
	"daxiang-vpn/frontend/dxvpn/internal/config"
	"daxiang-vpn/frontend/dxvpn/internal/netcheck"
	"daxiang-vpn/frontend/dxvpn/internal/paths"
	"daxiang-vpn/frontend/dxvpn/internal/proxy"
)

func Run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	ctx, err := paths.NewContext()
	if err != nil {
		return err
	}

	switch args[0] {
	case proxy.EngineCommand:
		return proxy.RunEngine(ctx)
	case "login":
		if len(args) != 2 {
			return errors.New("用法：dxvpn.exe login <授权码>")
		}
		return login(ctx, args[1])
	case "import":
		if len(args) != 2 {
			return errors.New("用法：dxvpn.exe import <配置文件>")
		}
		return importConfig(ctx, args[1])
	case "start":
		return start(ctx, args[1:])
	case "stop":
		return stop(ctx)
	case "status":
		return status(ctx)
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("未知命令：%s", args[0])
	}
}

func printUsage() {
	fmt.Println(`大象 VPN CLI

用法：
  dxvpn.exe login <授权码>
  dxvpn.exe start [--port <端口>]
  dxvpn.exe status
  dxvpn.exe stop
  dxvpn.exe help

本地代理端口默认 7890，可临时指定其它端口：
  dxvpn.exe start --port 7891
也可用环境变量 DXVPN_LOCAL_PORT 指定（--port 优先）。`)
}

// parsePortOverride resolves the local proxy port override for `start`.
// Precedence: --port flag > DXVPN_LOCAL_PORT env > 0 (use Hub/config value).
func parsePortOverride(args []string) (int, error) {
	portText := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--port":
			if i+1 >= len(args) {
				return 0, errors.New("--port 需要一个端口号")
			}
			portText = args[i+1]
			i++
		case strings.HasPrefix(arg, "--port="):
			portText = strings.TrimPrefix(arg, "--port=")
		default:
			return 0, fmt.Errorf("未知参数：%s", arg)
		}
	}
	if portText == "" {
		portText = strings.TrimSpace(os.Getenv("DXVPN_LOCAL_PORT"))
	}
	if portText == "" {
		return 0, nil
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("端口无效：%s", portText)
	}
	return port, nil
}

func login(ctx paths.Context, token string) error {
	if token == "" {
		return errors.New("授权码不能为空")
	}
	if err := ctx.EnsureDirs(); err != nil {
		return err
	}
	cfg, err := bootstrap.Fetch(token)
	if err != nil {
		return err
	}
	if err := config.Save(ctx.ConfigPath, config.Config{
		License: config.LicenseConfig{Token: token},
	}); err != nil {
		return err
	}

	fmt.Println("授权成功")
	fmt.Printf("出口：%s\n", cfg.Egress.CustomerName())
	fmt.Printf("代理：http://%s\n", cfg.LocalProxy.Addr())
	return nil
}

func importConfig(ctx paths.Context, source string) error {
	cfg, err := config.Load(source)
	if err != nil {
		return err
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := ctx.EnsureDirs(); err != nil {
		return err
	}
	if err := config.Save(ctx.ConfigPath, cfg); err != nil {
		return err
	}

	fmt.Println("配置已导入")
	fmt.Printf("客户端：%s\n", cfg.Client.Name)
	fmt.Printf("默认出口：%s\n", cfg.Egress.CustomerName())
	fmt.Printf("代理：%s\n", cfg.LocalProxy.Addr())
	return nil
}

func loadInstalledConfig(ctx paths.Context) (config.Config, error) {
	cfg, err := config.Load(ctx.ConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return config.Config{}, errors.New("未找到配置，请先执行 dxvpn.exe login <授权码>")
		}
		return config.Config{}, err
	}
	if cfg.License.Token != "" {
		cfg, err = bootstrap.Fetch(cfg.License.Token)
		if err != nil {
			return config.Config{}, err
		}
	}
	return cfg, cfg.Validate()
}

func start(ctx paths.Context, args []string) error {
	portOverride, err := parsePortOverride(args)
	if err != nil {
		return err
	}

	cfg, err := loadInstalledConfig(ctx)
	if err != nil {
		return err
	}
	if portOverride != 0 {
		cfg.LocalProxy.ListenPort = portOverride
		if err := cfg.Validate(); err != nil {
			return err
		}
	}
	if err := ctx.EnsureDirs(); err != nil {
		return err
	}

	if reachable, _ := netcheck.TCP(cfg.LocalProxy.Addr(), netcheck.ShortTimeout); reachable {
		fmt.Println("已在运行")
		printStartSummary(cfg)
		return nil
	}

	if running, _ := proxy.IsRunning(ctx); running {
		fmt.Println("已在运行")
		printStartSummary(cfg)
		return nil
	}

	if err := proxy.WriteSingBoxConfig(ctx, cfg); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(ctx.SingBoxConfig)
	}()
	if err := proxy.Start(ctx, cfg); err != nil {
		return err
	}
	if !waitForTCP(cfg.LocalProxy.Addr(), 8*time.Second) {
		return errors.New("代理启动失败，请重试")
	}

	fmt.Println("已启动")
	printStartSummary(cfg)
	return nil
}

func waitForTCP(addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if reachable, _ := netcheck.TCP(addr, 500*time.Millisecond); reachable {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return false
}

func printStartSummary(cfg config.Config) {
	fmt.Printf("代理：http://%s\n", cfg.LocalProxy.Addr())
	fmt.Printf("出口：%s\n", cfg.Egress.CustomerName())
}

func stop(ctx paths.Context) error {
	stopped, err := proxy.Stop(ctx)
	if err != nil {
		return err
	}
	if stopped {
		fmt.Println("已停止")
	} else {
		fmt.Println("未运行")
	}
	return nil
}

func status(ctx paths.Context) error {
	cfg, err := loadInstalledConfig(ctx)
	if err != nil {
		return err
	}

	running, _ := proxy.IsRunning(ctx)
	localReachable, _ := netcheck.TCP(cfg.LocalProxy.Addr(), netcheck.ShortTimeout)

	if running || localReachable {
		fmt.Println("状态：运行中")
	} else {
		fmt.Println("状态：未运行")
	}
	fmt.Printf("代理：%s", cfg.LocalProxy.Addr())
	printBool(localReachable)
	fmt.Printf("出口：%s\n", cfg.Egress.CustomerName())
	if localReachable {
		if ip, err := netcheck.PublicIPViaHTTPProxy(cfg.LocalProxy.Addr()); err == nil && ip != "" {
			fmt.Printf("出口 IP：%s\n", ip)
		} else {
			fmt.Println("出口 IP：获取失败")
		}
	} else {
		fmt.Println("出口 IP：未连接")
	}
	return nil
}

func printBool(ok bool) {
	if ok {
		fmt.Println(" 正常")
	} else {
		fmt.Println(" 未连接")
	}
}
