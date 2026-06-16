package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"zongheng-vpn/clients/cli/internal/bootstrap"
	"zongheng-vpn/clients/cli/internal/netcheck"
	"zongheng-vpn/shared/config"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
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
		return withOperationLock(ctx, func() error { return loginCmd(ctx, args[1:]) })
	case "import":
		if len(args) != 2 {
			return errors.New("用法：zhvpn.exe import <配置文件>")
		}
		return withOperationLock(ctx, func() error { return importConfig(ctx, args[1]) })
	case "start":
		return withOperationLock(ctx, func() error { return start(ctx, args[1:]) })
	case "stop":
		return withOperationLock(ctx, func() error { return stop(ctx, args[1:]) })
	case "status":
		return status(ctx, args[1:])
	case "rotate-ip":
		return rotateIP(ctx, args[1:])
	case "logout":
		return withOperationLock(ctx, func() error { return logout(ctx, args[1:]) })
	case "version":
		return version(args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("未知命令：%s", args[0])
	}
}

func printUsage() {
	fmt.Println(`纵横 VPN CLI

用法：
  zhvpn.exe login <授权码>
  zhvpn.exe start [--port <端口>] [--fast]
  zhvpn.exe status
  zhvpn.exe rotate-ip [--down-seconds <秒>] [--wait-seconds <秒>]
  zhvpn.exe stop
  zhvpn.exe logout
  zhvpn.exe version
  zhvpn.exe help

本地代理端口默认 7890，可临时指定其它端口：
  zhvpn.exe start --port 7891
也可用环境变量 ZHVPN_LOCAL_PORT 指定（--port 优先）。

--fast：高性能模式，使用系统网络栈（延迟更低、速度更快），
        需要管理员权限，启动时会弹出 UAC 授权窗口。

rotate-ip：更换当前手机卡出口 IP，并等待出口恢复。`)
}

// ErrSilent signals that a command already reported its outcome (e.g. as a JSON
// object on stdout). main exits non-zero without printing the error again.
var ErrSilent = errors.New("已输出结果")

// jsonResult is the machine-readable result for login / rotate-ip (--json).
type jsonResult struct {
	OK      bool   `json:"ok"`
	Status  string `json:"status,omitempty"`
	Egress  string `json:"egress,omitempty"`
	Proxy   string `json:"proxy,omitempty"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
	Message string `json:"message,omitempty"`
	Version string `json:"version,omitempty"`
	Error   string `json:"error,omitempty"`
}

// statusResult is the machine-readable result for status --json.
type statusResult struct {
	Running        bool   `json:"running"`
	Proxy          string `json:"proxy,omitempty"`
	ProxyReachable bool   `json:"proxy_reachable"`
	Egress         string `json:"egress,omitempty"`
	EgressIP       string `json:"egress_ip,omitempty"`
	EgressIPv4     string `json:"egress_ipv4,omitempty"`
	EgressIPv6     string `json:"egress_ipv6,omitempty"`
	Error          string `json:"error,omitempty"`
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false) // keep < > & literal so messages like "<授权码>" stay readable
	return enc.Encode(v)     // Encode appends a trailing newline
}

// reportErr renders err as {"ok":false,"error":...} in JSON mode (returning
// ErrSilent so main exits non-zero quietly), or returns it verbatim otherwise.
func reportErr(jsonOut bool, err error) error {
	if jsonOut {
		_ = printJSON(jsonResult{Error: err.Error()})
		return ErrSilent
	}
	return err
}

// wantJSON parses flag-only args, accepting just --json.
func wantJSON(args []string) (bool, error) {
	jsonOut := false
	for _, arg := range args {
		switch arg {
		case "--json":
			jsonOut = true
		default:
			return false, fmt.Errorf("未知参数：%s", arg)
		}
	}
	return jsonOut, nil
}

type statusOptions struct {
	jsonOut bool
	checkIP bool
}

func parseStatusOptions(args []string) (statusOptions, error) {
	opts := statusOptions{checkIP: true}
	for _, arg := range args {
		switch arg {
		case "--json":
			opts.jsonOut = true
		case "--no-ip-check":
			opts.checkIP = false
		default:
			return opts, fmt.Errorf("未知参数：%s", arg)
		}
	}
	return opts, nil
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

type startOptions struct {
	port    int  // 0 = use Hub/config value
	fast    bool // system TUN (admin)
	jsonOut bool
}

// parseStartOptions parses the `start` flags.
// Port precedence: --port flag > ZHVPN_LOCAL_PORT env > 0 (use Hub/config value).
func parseStartOptions(args []string) (startOptions, error) {
	var opts startOptions
	portText := ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--fast":
			opts.fast = true
		case arg == "--json":
			opts.jsonOut = true
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
		portText = strings.TrimSpace(os.Getenv("ZHVPN_LOCAL_PORT"))
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

func loginCmd(ctx paths.Context, args []string) error {
	jsonOut := false
	var positional []string
	for _, arg := range args {
		if arg == "--json" {
			jsonOut = true
			continue
		}
		positional = append(positional, arg)
	}
	if len(positional) != 1 {
		return errors.New("用法：zhvpn.exe login <授权码> [--json]")
	}
	return login(ctx, positional[0], jsonOut)
}

func login(ctx paths.Context, token string, jsonOut bool) error {
	cfg, err := doLogin(ctx, token)
	if err != nil {
		return reportErr(jsonOut, err)
	}
	if jsonOut {
		return printJSON(jsonResult{OK: true, Egress: cfg.Egress.CustomerName(), Proxy: cfg.LocalProxy.Addr()})
	}

	fmt.Println("授权成功")
	fmt.Printf("出口：%s\n", cfg.Egress.CustomerName())
	fmt.Printf("代理：http://%s\n", cfg.LocalProxy.Addr())
	return nil
}

func doLogin(ctx paths.Context, token string) (config.Config, error) {
	if token == "" {
		return config.Config{}, errors.New("授权码不能为空")
	}
	if err := ctx.EnsureDirs(); err != nil {
		return config.Config{}, err
	}
	cfg, err := bootstrap.Fetch(token)
	if err != nil {
		return config.Config{}, err
	}
	if err := saveClientConfigCache(ctx, cfg); err != nil {
		return config.Config{}, err
	}
	return cfg, nil
}

// logout removes the saved config (token), so the next status reports
// "not configured" and the GUI returns to the login screen.
func logout(ctx paths.Context, args []string) error {
	jsonOut, err := wantJSON(args)
	if err != nil {
		return err
	}
	if err := os.Remove(ctx.ConfigPath); err != nil && !os.IsNotExist(err) {
		return reportErr(jsonOut, err)
	}
	_ = os.Remove(runtimeFingerprintPath(ctx))
	if jsonOut {
		return printJSON(jsonResult{OK: true})
	}
	fmt.Println("已登出")
	return nil
}

func version(args []string) error {
	jsonOut, err := wantJSON(args)
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSON(jsonResult{OK: true, Version: Version})
	}
	fmt.Println(Version)
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

func loadLocalInstalledConfig(ctx paths.Context) (config.Config, error) {
	cfg, err := config.Load(ctx.ConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return config.Config{}, errors.New("未找到配置，请先执行 zhvpn.exe login <授权码>")
		}
		return config.Config{}, err
	}
	return cfg, nil
}

func loadInstalledConfig(ctx paths.Context) (config.Config, error) {
	cfg, err := loadLocalInstalledConfig(ctx)
	if err != nil {
		return config.Config{}, err
	}
	if clientConfigCacheNeedsBootstrap(cfg) {
		return refreshInstalledConfig(ctx, cfg)
	}
	return cfg, cfg.Validate()
}

func refreshInstalledConfig(ctx paths.Context, cached config.Config) (config.Config, error) {
	if strings.TrimSpace(cached.License.Token) == "" {
		return cached, cached.Validate()
	}
	cfg, err := bootstrap.Fetch(cached.License.Token)
	if err != nil {
		return config.Config{}, err
	}
	if err := saveClientConfigCache(ctx, cfg); err != nil {
		return config.Config{}, err
	}
	return cfg, cfg.Validate()
}

func clientConfigCacheNeedsBootstrap(cfg config.Config) bool {
	if strings.TrimSpace(cfg.License.Token) == "" {
		return false
	}
	required := []string{
		cfg.Client.Name,
		cfg.Hub.Endpoint,
		cfg.Egress.Name,
		cfg.Egress.ManagementAddr,
		cfg.Egress.ProxyAddr,
	}
	for _, value := range required {
		if strings.TrimSpace(value) == "" {
			return true
		}
	}
	return false
}

func saveClientConfigCache(ctx paths.Context, cfg config.Config) error {
	if err := os.MkdirAll(filepath.Dir(ctx.ConfigPath), 0700); err != nil {
		return err
	}
	cache := cfg
	// The local status cache only needs routing metadata. Keep the WireGuard
	// private key in memory for the current start/login command, not on disk.
	cache.WireGuard.PrivateKey = ""
	return config.Save(ctx.ConfigPath, cache)
}

var Version = "dev"

func start(ctx paths.Context, args []string) error {
	jsonOut := hasFlag(args, "--json")
	opts, err := parseStartOptions(args)
	if err != nil {
		return reportErr(jsonOut, err)
	}

	cached, err := loadLocalInstalledConfig(ctx)
	if err != nil {
		return reportErr(opts.jsonOut, err)
	}
	cfg, err := refreshInstalledConfig(ctx, cached)
	if err != nil {
		return reportErr(opts.jsonOut, err)
	}
	if opts.port != 0 {
		cfg.LocalProxy.ListenPort = opts.port
		if err := cfg.Validate(); err != nil {
			return reportErr(opts.jsonOut, err)
		}
	}
	if err := ctx.EnsureDirs(); err != nil {
		return reportErr(opts.jsonOut, err)
	}

	running, _ := proxy.IsRunning(ctx)
	localReachable, _ := netcheck.TCP(cfg.LocalProxy.Addr(), netcheck.ShortTimeout)
	if running && localReachable && runtimeConfigMatches(ctx, cfg, opts.fast) {
		if opts.jsonOut {
			return printJSON(jsonResult{OK: true, Egress: cfg.Egress.CustomerName(), Proxy: cfg.LocalProxy.Addr(), Message: "已在运行"})
		}
		fmt.Println("已在运行")
		printStartSummary(cfg)
		return nil
	}
	if running {
		if !opts.jsonOut {
			fmt.Println("检测到运行配置已变化，正在重启代理")
		}
		if _, err := proxy.Stop(ctx); err != nil {
			return reportErr(opts.jsonOut, err)
		}
	} else if localReachable {
		return reportErr(opts.jsonOut, fmt.Errorf("本地代理端口 %s 已被占用，请先关闭旧进程或换端口", cfg.LocalProxy.Addr()))
	}

	if err := proxy.WriteSingBoxConfig(ctx, cfg, opts.fast); err != nil {
		return reportErr(opts.jsonOut, err)
	}
	defer func() {
		_ = os.Remove(ctx.SingBoxConfig)
	}()
	if err := proxy.Start(ctx, cfg, opts.fast); err != nil {
		return reportErr(opts.jsonOut, err)
	}
	// System TUN (--fast) takes longer to come up (driver + interface setup).
	timeout := 8 * time.Second
	if opts.fast {
		timeout = 20 * time.Second
	}
	if !waitForTCP(cfg.LocalProxy.Addr(), timeout) {
		return reportErr(opts.jsonOut, errors.New("代理启动失败，请重试"))
	}
	if err := writeRuntimeFingerprint(ctx, cfg, opts.fast); err != nil {
		return reportErr(opts.jsonOut, err)
	}

	if opts.jsonOut {
		return printJSON(jsonResult{OK: true, Egress: cfg.Egress.CustomerName(), Proxy: cfg.LocalProxy.Addr(), Message: "已启动"})
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

func stop(ctx paths.Context, args []string) error {
	jsonOut, err := wantJSON(args)
	if err != nil {
		return err
	}
	stopped, err := proxy.Stop(ctx)
	if err != nil {
		return reportErr(jsonOut, err)
	}
	_ = os.Remove(runtimeFingerprintPath(ctx))
	if stopped {
		if jsonOut {
			return printJSON(jsonResult{OK: true, Message: "已停止"})
		}
		fmt.Println("已停止")
	} else {
		if jsonOut {
			return printJSON(jsonResult{OK: true, Message: "未运行"})
		}
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

func status(ctx paths.Context, args []string) error {
	opts, err := parseStatusOptions(args)
	if err != nil {
		return err
	}

	cfg, err := loadInstalledConfig(ctx)
	if err != nil {
		if opts.jsonOut {
			_ = printJSON(statusResult{Error: err.Error()})
			return ErrSilent
		}
		return err
	}

	running, _ := proxy.IsRunning(ctx)
	localReachable, _ := netcheck.TCP(cfg.LocalProxy.Addr(), netcheck.ShortTimeout)

	if opts.jsonOut {
		res := statusResult{
			Running:        running || localReachable,
			Proxy:          cfg.LocalProxy.Addr(),
			ProxyReachable: localReachable,
			Egress:         cfg.Egress.CustomerName(),
		}
		if localReachable {
			if opts.checkIP {
				if ips, err := netcheck.PublicIPsViaHTTPProxy(cfg.LocalProxy.Addr()); err == nil {
					res.EgressIPv4 = ips.IPv4
					res.EgressIPv6 = ips.IPv6
					if publicIPMatchesEndpoint(res.EgressIPv4, cfg.Hub.Endpoint) {
						res.EgressIPv4 = ""
					}
					if ips.IPv6 != "" {
						res.EgressIP = ips.IPv6
					} else {
						res.EgressIP = res.EgressIPv4
					}
				}
			}
		}
		return printJSON(res)
	}

	if running || localReachable {
		fmt.Println("状态：运行中")
	} else {
		fmt.Println("状态：未运行")
	}
	fmt.Printf("代理：%s", cfg.LocalProxy.Addr())
	printBool(localReachable)
	fmt.Printf("出口：%s\n", cfg.Egress.CustomerName())
	if localReachable {
		if opts.checkIP {
			if ips, err := netcheck.PublicIPsViaHTTPProxy(cfg.LocalProxy.Addr()); err == nil {
				if publicIPMatchesEndpoint(ips.IPv4, cfg.Hub.Endpoint) {
					ips.IPv4 = ""
				}
				if ips.IPv6 != "" {
					fmt.Printf("出口 IPv6：%s\n", ips.IPv6)
				}
				if ips.IPv4 != "" {
					fmt.Printf("出口 IPv4：%s\n", ips.IPv4)
				}
				if ips.IPv4 == "" && ips.IPv6 == "" {
					fmt.Println("出口 IP：获取失败")
				}
			} else {
				fmt.Println("出口 IP：获取失败")
			}
		} else {
			fmt.Println("出口 IP：未探测")
		}
	} else {
		fmt.Println("出口 IP：未连接")
	}
	return nil
}

func publicIPMatchesEndpoint(ip, endpoint string) bool {
	if ip == "" {
		return false
	}
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		host = endpoint
	}
	return net.ParseIP(ip) != nil && net.ParseIP(host) != nil && net.ParseIP(ip).Equal(net.ParseIP(host))
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
		case arg == "--json":
			// 机器可读输出，由调用方处理；不影响换 IP 行为。
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
	jsonOut := hasFlag(args, "--json")
	cfg, err := loadInstalledConfig(ctx)
	if err != nil {
		return reportErr(jsonOut, err)
	}
	opts, err := parseRotateIPOptions(args, cfg)
	if err != nil {
		return reportErr(jsonOut, err)
	}

	res, err := performRotate(cfg, opts, jsonOut)
	if err != nil {
		return reportErr(jsonOut, err)
	}

	if jsonOut {
		return printJSON(jsonResult{
			OK:      true,
			Status:  res.status,
			Egress:  cfg.Egress.CustomerName(),
			Before:  res.before,
			After:   res.after,
			Message: res.message,
		})
	}
	if res.status == "busy" {
		fmt.Println(res.message)
		return nil
	}
	fmt.Printf("换 IP 后出口：%s\n", res.after)
	if res.after == unavailableIP {
		fmt.Println("提示：出口可能还没恢复，过几秒再执行 zhvpn.exe status，或加大 --wait-seconds。")
	}
	return nil
}

type rotateOutcome struct {
	before  string
	after   string
	status  string
	message string
}

// performRotate triggers a rotate (hub-managed or direct ssh) and returns the
// public egress IP before and after. When quiet is true (--json) it suppresses
// all progress output so stdout carries only the caller's final JSON.
func performRotate(cfg config.Config, opts rotateIPOptions, quiet bool) (rotateOutcome, error) {
	before := publicIPOrUnavailable(opts.proxyAddr)
	if !quiet {
		fmt.Printf("换 IP 前出口：%s\n", before)
	}

	if !opts.direct {
		if !quiet {
			fmt.Printf("触发 Android rotate-ip（断网 %ds）...\n", opts.downSeconds)
		}
		rotate, err := bootstrap.RotateIP(cfg.License.Token, opts.downSeconds)
		if err != nil {
			return rotateOutcome{before: before}, err
		}
		if rotate.Status == "busy" {
			message := rotate.Message
			if message == "" {
				message = "换 IP 正在进行中，请稍后再试"
			}
			if rotate.RetryAfterSeconds > 0 {
				message = fmt.Sprintf("%s（约 %d 秒后可重试）", message, rotate.RetryAfterSeconds)
			}
			return rotateOutcome{before: before, after: before, status: "busy", message: message}, nil
		}
		return rotateOutcome{
			before: before,
			after:  waitForPublicIP(opts.proxyAddr, opts.downSeconds, opts.waitSeconds, quiet),
			status: "triggered",
		}, nil
	}

	if _, err := os.Stat(opts.keyPath); err != nil {
		if os.IsNotExist(err) {
			return rotateOutcome{before: before}, fmt.Errorf("Android 控制面 SSH 私钥不存在：%s\n请把当前授权的私钥放到该路径，或使用 --key <路径>。普通用户态 start 还需要 --jump root@36.50.84.68 或改用 start --fast 提供系统路由", opts.keyPath)
		}
		return rotateOutcome{before: before}, fmt.Errorf("无法读取 Android 控制面 SSH 私钥：%w", err)
	}
	if opts.jumpHost == "" {
		controlAddr := net.JoinHostPort(opts.phone, strconv.Itoa(opts.port))
		if reachable, _ := netcheck.TCP(controlAddr, 3*time.Second); !reachable {
			return rotateOutcome{before: before}, fmt.Errorf("Android 控制面 %s 不可达。普通 zhvpn.exe start 是用户态代理模式，不会给 Windows 系统添加 10.66.0.0/24 路由；请使用 --jump root@36.50.84.68，或先用 zhvpn.exe start --fast / 管理内网 WireGuard 提供系统路由", controlAddr)
		}
	}
	if !quiet {
		fmt.Printf("触发 Android rotate-ip（断网 %ds）...\n", opts.downSeconds)
	}

	remote := fmt.Sprintf("sh /data/adb/zhandroid/rotate-ip.sh %d", opts.downSeconds)
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
	if !quiet {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return rotateOutcome{before: before}, fmt.Errorf("触发 rotate-ip 失败：%w", err)
	}

	return rotateOutcome{
		before: before,
		after:  waitForPublicIP(opts.proxyAddr, opts.downSeconds, opts.waitSeconds, quiet),
		status: "triggered",
	}, nil
}

const unavailableIP = "(暂不可达)"

func waitForPublicIP(proxyAddr string, downSeconds, waitSeconds int, quiet bool) string {
	if !quiet {
		fmt.Printf("最多等待 %ds 让无线电重注册 + 隧道恢复...\n", waitSeconds)
	}
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
	for _, name := range []string{"zhandroid_control", "zhandroid_control_local"} {
		path := filepath.Join(home, ".ssh", name)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return filepath.Join(home, ".ssh", "zhandroid_control")
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
