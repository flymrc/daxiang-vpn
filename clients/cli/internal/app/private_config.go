package app

import (
	"zongheng-vpn/shared/config"
	"zongheng-vpn/shared/paths"
	"zongheng-vpn/shared/proxy"
)

func savePrivateConfig(home paths.Context, cfg config.Config) error {
	state, err := proxy.NewPrivateState(home, "config.yaml")
	if err != nil {
		return err
	}
	data, err := config.Encode(cfg)
	if err != nil {
		return err
	}
	return state.Write(data)
}

func loadPrivateConfig(home paths.Context) (config.Config, error) {
	state, err := proxy.NewPrivateState(home, "config.yaml")
	if err != nil {
		return config.Config{}, err
	}
	data, err := state.Read()
	if err != nil {
		return config.Config{}, err
	}
	return config.Decode(data)
}
