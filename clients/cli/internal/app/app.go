package app

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"daxiang-vpn/clients/cli/internal/bootstrap"
	"daxiang-vpn/clients/cli/internal/netcheck"
	"daxiang-vpn/shared/config"
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
	case "rotate-ip":
		return rotateIP(ctx, args[1:])
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
  dxvpn.exe rotate-ip [--down-seconds <秒>] [--wait-seconds <秒>]
  dxvpn.exe stop
  dxvpn.exe help

本地代理端口默认 7890，可临时指定其它端口：
  dxvpn.exe start --port 7891
也可用环境变量 DXVPN_LOCAL_PORT 指定（--port 优先）。

--fast：高性能模式，使用系统网络栈（延迟更低、速度更快），
        需要管理员权限，启动时会弹出 UAC 授权窗口。

rotate-ip：经 Android 控制面 SSH 触发手机端 rotate-ip.sh，
           用于让手机网络重注册并尝试更换公网出口 IP。
           默认由 Hub 代为触发，客户端不需要 Android SSH 私钥。
           管理员排障可加 --direct --jump/--key 直连控制面。`)
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

type rotateIPOptions struct {
	downSeconds int
	waitSeconds int
	phone       string
	port        int
	keyPath     string
	proxyAddr   string
	jumpHost    string
	direct      bool
}

func parseRotateIPOptions(args []string, cfg config.Config) (rotateIPOptions, error) {
	opts := rotateIPOptions{
		downSeconds: 8,
		waitSeconds: 75,
		port:        2022,
		keyPath:     defaultAndroidControlKeyPath(),
		proxyAddr:   cfg.LocalProxy.Addr(),
	}
	if cfg.Egress.ManagementAddr != "" {
		host, port, err := splitOptionalHostPort(cfg.Egress.ManagementAddr, opts.port)
		if err != nil {
			return opts, err
		}
		opts.phone = host
		opts.port = port
	}
	if opts.phone == "" {
		opts.phone = "10.66.0.101"
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]
		readValue := func(name string) (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s 需要一个值", name)
			}
			i++
			return args[i], nil
		}
		parsePositiveInt := func(name, text string) (int, error) {
			value, err := strconv.Atoi(text)
			if err != nil || value < 1 {
				return 0, fmt.Errorf("%s 必须是正整数：%s", name, text)
			}
			return value, nil
		}

		switch {
		case arg == "--down-seconds":
			value, err := readValue("--down-seconds")
			if err != nil {
				return opts, err
			}
			seconds, err := parsePositiveInt("--down-seconds", value)
			if err != nil {
				return opts, err
			}
			opts.downSeconds = seconds
		case strings.HasPrefix(arg, "--down-seconds="):
			seconds, err := parsePositiveInt("--down-seconds", strings.TrimPrefix(arg, "--down-seconds="))
			if err != nil {
				return opts, err
			}
			opts.downSeconds = seconds
		case arg == "--wait-seconds":
			value, err := readValue("--wait-seconds")
			if err != nil {
				return opts, err
			}
			seconds, err := parsePositiveInt("--wait-seconds", value)
			if err != nil {
				return opts, err
			}
			opts.waitSeconds = seconds
		case strings.HasPrefix(arg, "--wait-seconds="):
			seconds, err := parsePositiveInt("--wait-seconds", strings.TrimPrefix(arg, "--wait-seconds="))
			if err != nil {
				return opts, err
			}
			opts.waitSeconds = seconds
		case arg == "--phone":
			value, err := readValue("--phone")
			if err != nil {
				return opts, err
			}
			opts.phone = strings.TrimSpace(value)
			opts.direct = true
		case strings.HasPrefix(arg, "--phone="):
			opts.phone = strings.TrimSpace(strings.TrimPrefix(arg, "--phone="))
			opts.direct = true
		case arg == "--port":
			value, err := readValue("--port")
			if err != nil {
				return opts, err
			}
			port, err := parsePort(value)
			if err != nil {
				return opts, err
			}
			opts.port = port
			opts.direct = true
		case strings.HasPrefix(arg, "--port="):
			port, err := parsePort(strings.TrimPrefix(arg, "--port="))
			if err != nil {
				return opts, err
			}
			opts.port = port
			opts.direct = true
		case arg == "--key":
			value, err := readValue("--key")
			if err != nil {
				return opts, err
			}
			opts.keyPath = expandHome(value)
			opts.direct = true
		case strings.HasPrefix(arg, "--key="):
			opts.keyPath = expandHome(strings.TrimPrefix(arg, "--key="))
			opts.direct = true
		case arg == "--proxy":
			value, err := readValue("--proxy")
			if err != nil {
				return opts, err
			}
			opts.proxyAddr = normalizeProxyAddr(value)
		case strings.HasPrefix(arg, "--proxy="):
			opts.proxyAddr = normalizeProxyAddr(strings.TrimPrefix(arg, "--proxy="))
		case arg == "--jump":
			value, err := readValue("--jump")
			if err != nil {
				return opts, err
			}
			opts.jumpHost = strings.TrimSpace(value)
			opts.direct = true
		case strings.HasPrefix(arg, "--jump="):
			opts.jumpHost = strings.TrimSpace(strings.TrimPrefix(arg, "--jump="))
			opts.direct = true
		case arg == "--direct":
			opts.direct = true
		default:
			return opts, fmt.Errorf("未知参数：%s", arg)
		}
	}

	if opts.phone == "" {
		return opts, errors.New("Android 控制面地址不能为空")
	}
	if opts.keyPath == "" {
		return opts, errors.New("SSH 私钥路径不能为空")
	}
	if opts.proxyAddr == "" {
		return opts, errors.New("代理地址不能为空")
	}
	return opts, nil
}

func rotateIP(ctx paths.Context, args []string) error {
	cfg, err := loadInstalledConfig(ctx)
	if err != nil {
		return err
	}
	opts, err := parseRotateIPOptions(args, cfg)
	if err != nil {
		return err
	}

	before := publicIPOrUnavailable(opts.proxyAddr)
	fmt.Printf("换 IP 前出口：%s\n", before)
	if !opts.direct {
		fmt.Printf("触发 Android rotate-ip（断网 %ds）...\n", opts.downSeconds)
		if err := bootstrap.RotateIP(cfg.License.Token, opts.downSeconds); err != nil {
			return err
		}
		after := waitForPublicIP(opts.proxyAddr, opts.downSeconds, opts.waitSeconds)
		fmt.Printf("换 IP 后出口：%s\n", after)
		if after == unavailableIP {
			fmt.Println("提示：出口可能还没恢复，过几秒再执行 dxvpn.exe status，或加大 --wait-seconds。")
		}
		return nil
	}
	if _, err := os.Stat(opts.keyPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("Android 控制面 SSH 私钥不存在：%s\n请把当前授权的私钥放到该路径，或使用 --key <路径>。普通用户态 start 还需要 --jump root@36.50.84.68 或改用 start --fast 提供系统路由", opts.keyPath)
		}
		return fmt.Errorf("无法读取 Android 控制面 SSH 私钥：%w", err)
	}
	if opts.jumpHost == "" {
		controlAddr := net.JoinHostPort(opts.phone, strconv.Itoa(opts.port))
		if reachable, _ := netcheck.TCP(controlAddr, 3*time.Second); !reachable {
			return fmt.Errorf("Android 控制面 %s 不可达。普通 dxvpn.exe start 是用户态代理模式，不会给 Windows 系统添加 10.66.0.0/24 路由；请使用 --jump root@36.50.84.68，或先用 dxvpn.exe start --fast / 管理内网 WireGuard 提供系统路由", controlAddr)
		}
	}
	fmt.Printf("触发 Android rotate-ip（断网 %ds）...\n", opts.downSeconds)

	remote := fmt.Sprintf("sh /data/adb/dxandroid/rotate-ip.sh %d", opts.downSeconds)
	sshArgs := []string{
		"-i", opts.keyPath,
		"-p", strconv.Itoa(opts.port),
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + os.DevNull,
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=8",
	}
	if opts.jumpHost != "" {
		sshArgs = append(sshArgs, "-J", opts.jumpHost)
	}
	sshArgs = append(sshArgs, "root@"+opts.phone, remote)
	cmd := exec.Command("ssh", sshArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("触发 rotate-ip 失败：%w", err)
	}

	after := waitForPublicIP(opts.proxyAddr, opts.downSeconds, opts.waitSeconds)
	fmt.Printf("换 IP 后出口：%s\n", after)
	if after == unavailableIP {
		fmt.Println("提示：出口可能还没恢复，过几秒再执行 dxvpn.exe status，或加大 --wait-seconds。")
	}
	return nil
}

const unavailableIP = "(暂不可达)"

func waitForPublicIP(proxyAddr string, downSeconds, waitSeconds int) string {
	fmt.Printf("最多等待 %ds 让无线电重注册 + 隧道恢复...\n", waitSeconds)
	deadline := time.Now().Add(time.Duration(waitSeconds) * time.Second)
	initialDelay := time.Duration(downSeconds+10) * time.Second
	if initialDelay > time.Duration(waitSeconds)*time.Second {
		initialDelay = time.Duration(waitSeconds) * time.Second
	}
	time.Sleep(initialDelay)

	interval := 5 * time.Second
	for {
		ip := publicIPOrUnavailable(proxyAddr)
		if ip != unavailableIP {
			return ip
		}
		if time.Now().Add(interval).After(deadline) {
			break
		}
		time.Sleep(interval)
	}
	return publicIPOrUnavailable(proxyAddr)
}

func publicIPOrUnavailable(proxyAddr string) string {
	ip, err := netcheck.PublicIPViaHTTPProxy(normalizeProxyAddr(proxyAddr))
	if err != nil || ip == "" {
		return unavailableIP
	}
	return ip
}

func normalizeProxyAddr(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "http://")
	return strings.TrimPrefix(value, "https://")
}

func splitOptionalHostPort(value string, defaultPort int) (string, int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", defaultPort, nil
	}
	host, portText, err := net.SplitHostPort(value)
	if err != nil {
		if strings.Contains(value, ":") {
			return "", 0, fmt.Errorf("egress.management_addr 格式错误，应类似 10.66.0.101 或 10.66.0.101:2022")
		}
		return value, defaultPort, nil
	}
	port, err := parsePort(portText)
	if err != nil {
		return "", 0, err
	}
	return host, port, nil
}

func parsePort(value string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("端口无效：%s", value)
	}
	return port, nil
}

func defaultAndroidControlKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	for _, name := range []string{"dxandroid_control", "dxandroid_control_local"} {
		path := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return filepath.Join(home, ".ssh", "dxandroid_control")
}

func expandHome(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
