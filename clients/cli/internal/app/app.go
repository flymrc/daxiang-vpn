package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"daxiang-vpn/clients/cli/internal/bootstrap"
	"daxiang-vpn/shared/config"
	"daxiang-vpn/clients/cli/internal/netcheck"
	"daxiang-vpn/shared/paths"
	"daxiang-vpn/shared/proxy"
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
		return proxy.RunEngine(engineContext(ctx, args[1:]))
	case proxy.KillCommand:
		if len(args) < 2 {
			return errors.New("缺少 pid")
		}
		pid, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("无效 pid：%s", args[1])
		}
		return proxy.KillPID(pid)
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
  dxvpn.exe start [--port <端口>] [--fast]
  dxvpn.exe status
  dxvpn.exe stop
  dxvpn.exe help

本地代理端口默认 7890，可临时指定其它端口：
  dxvpn.exe start --port 7891
也可用环境变量 DXVPN_LOCAL_PORT 指定（--port 优先）。

--fast：高性能模式，使用系统网络栈（延迟更低、速度更快），
        需要管理员权限，启动时会弹出 UAC 授权窗口。`)
}

type startOptions struct {
	port int  // 0 = use Hub/config value
	fast bool // system TUN (admin)
}

// parseStartOptions parses the `start` flags.
// Port precedence: --port flag > DXVPN_LOCAL_PORT env > 0 (use Hub/config value).
func parseStartOptions(args []string) (startOptions, error) {
	var opts startOptions
	portText := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--fast":
			opts.fast = true
		case arg == "--port":
			if i+1 >= len(args) {
				return opts, errors.New("--port 需要一个端口号")
			}
			portText = args[i+1]
			i++
		case strings.HasPrefix(arg, "--port="):
			portText = strings.TrimPrefix(arg, "--port=")
		default:
			return opts, fmt.Errorf("未知参数：%s", arg)
		}
	}
	if portText == "" {
		portText = strings.TrimSpace(os.Getenv("DXVPN_LOCAL_PORT"))
	}
	if portText != "" {
		port, err := strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return opts, fmt.Errorf("端口无效：%s", portText)
		}
		opts.port = port
	}
	return opts, nil
}

// engineContext resolves the context for the engine child, honoring --home so
// an elevated engine uses the launching user's paths.
func engineContext(def paths.Context, args []string) paths.Context {
	for i := 0; i < len(args); i++ {
		if args[i] == proxy.HomeFlag && i+1 < len(args) {
			return paths.FromRoot(args[i+1])
		}
	}
	return def
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
	opts, err := parseStartOptions(args)
	if err != nil {
		return err
	}

	cfg, err := loadInstalledConfig(ctx)
	if err != nil {
		return err
	}
	if opts.port != 0 {
		cfg.LocalProxy.ListenPort = opts.port
		if err := cfg.Validate(); err != nil {
			return err
		}
	}
	if err := ctx.EnsureDirs(); err != nil {
		return err
	}

	running, _ := proxy.IsRunning(ctx)
	localReachable, _ := netcheck.TCP(cfg.LocalProxy.Addr(), netcheck.ShortTimeout)
	if running && localReachable && runtimeConfigMatches(ctx, cfg, opts.fast) {
		fmt.Println("已在运行")
		printStartSummary(cfg)
		return nil
	}
	if running {
		fmt.Println("检测到运行配置已变化，正在重启代理")
		if _, err := proxy.Stop(ctx); err != nil {
			return err
		}
	} else if localReachable {
		return fmt.Errorf("本地代理端口 %s 已被占用，请先关闭旧进程或换端口", cfg.LocalProxy.Addr())
	}

	if err := proxy.WriteSingBoxConfig(ctx, cfg, opts.fast); err != nil {
		return err
	}
	defer func() {
		_ = os.Remove(ctx.SingBoxConfig)
	}()
	if err := proxy.Start(ctx, cfg, opts.fast); err != nil {
		return err
	}
	// System TUN (--fast) takes longer to come up (driver + interface setup).
	timeout := 8 * time.Second
	if opts.fast {
		timeout = 20 * time.Second
	}
	if !waitForTCP(cfg.LocalProxy.Addr(), timeout) {
		return errors.New("代理启动失败，请重试")
	}
	if err := writeRuntimeFingerprint(ctx, cfg, opts.fast); err != nil {
		return err
	}

	fmt.Println("已启动")
	if opts.fast {
		fmt.Println("模式：高性能（系统网络栈）")
	}
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
	_ = os.Remove(runtimeFingerprintPath(ctx))
	if stopped {
		fmt.Println("已停止")
	} else {
		fmt.Println("未运行")
	}
	return nil
}

func runtimeConfigMatches(ctx paths.Context, cfg config.Config, fast bool) bool {
	data, err := os.ReadFile(runtimeFingerprintPath(ctx))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == runtimeFingerprint(cfg, fast)
}

func writeRuntimeFingerprint(ctx paths.Context, cfg config.Config, fast bool) error {
	return os.WriteFile(runtimeFingerprintPath(ctx), []byte(runtimeFingerprint(cfg, fast)+"\n"), 0600)
}

func runtimeFingerprintPath(ctx paths.Context) string {
	return filepath.Join(ctx.RunDir, "runtime.sha256")
}

func runtimeFingerprint(cfg config.Config, fast bool) string {
	text := strings.Join([]string{
		cfg.Hub.Endpoint,
		cfg.Hub.PublicKey,
		cfg.Egress.Name,
		cfg.Egress.ProxyAddr,
		cfg.LocalProxy.Addr(),
		cfg.WireGuard.Address,
		cfg.WireGuard.PrivateKey,
		strconv.FormatBool(fast),
	}, "\n")
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
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
