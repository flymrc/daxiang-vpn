package app

import (
	"context"
	"fmt"
	"zongheng-vpn/clients/cli/internal/runtime/leasecontrol"
	"zongheng-vpn/shared/contracts"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
)

func systemProxy(ctx paths.Context, args []string) error {
	jsonOut := hasFlag(args, "--json")
	if len(args) == 0 {
		return reportErr(jsonOut, fmt.Errorf("用法：system-proxy acquire|release|inspect|recover [--lease-id <ID>] [--json]"))
	}
	command := args[0]
	leaseID := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
		case "--lease-id":
			if i+1 >= len(args) {
				return reportErr(jsonOut, fmt.Errorf("--lease-id 缺少值"))
			}
			i++
			leaseID = args[i]
		default:
			return reportErr(jsonOut, fmt.Errorf("未知参数：%s", args[i]))
		}
	}
	if (command == "release") != (leaseID != "") {
		return reportErr(jsonOut, fmt.Errorf("仅 release 必须指定 --lease-id"))
	}
	var result contracts.Result
	var err error
	switch command {
	case "inspect":
		observation, e := leasecontrol.Inspect(context.Background(), ctx)
		err = e
		result = contracts.Result{OK: err == nil, SystemProxyState: observation.State, LeaseID: observation.LeaseID, Owned: contracts.Bool(false), Noop: contracts.Bool(true), JournalPath: observation.JournalPath}
	case "recover":
		result, err = leasecontrol.Recover(context.Background(), ctx)
	case "acquire", "release":
		var scope string
		scope, err = leasecontrol.UserScope(context.Background(), ctx)
		if err == nil {
			result, err = proxy.RequestRuntimeAction(ctx, proxy.RuntimeAction{Command: "system-proxy-" + command, UserScope: scope, LeaseID: leaseID})
		}
	default:
		err = fmt.Errorf("未知 system-proxy 动作：%s", command)
	}
	if err != nil {
		if jsonOut && result.Error != "" {
			_ = printJSON(result)
			return ErrSilent
		}
		return reportErr(jsonOut, err)
	}
	if jsonOut {
		return printJSON(result)
	}
	fmt.Printf("系统代理租约：%s\n", result.SystemProxyState)
	return nil
}
