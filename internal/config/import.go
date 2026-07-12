package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// ImportXray builds a Config from an existing Xray/v2ray client config.
//
// This exists so that migrating off an Xray or Amnezia setup does not mean
// re-typing a UUID and a REALITY key by hand — the step where people quietly
// transpose a character and then spend an evening debugging a handshake.
func ImportXray(path string) (Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}

	var doc xrayConfig
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}

	for _, ob := range doc.Outbounds {
		if ob.Protocol != "vless" {
			continue
		}
		for _, next := range ob.Settings.VNext {
			if len(next.Users) == 0 {
				continue
			}
			user := next.Users[0]
			reality := ob.StreamSettings.RealitySettings

			cfg := Default()
			cfg.Server = Server{
				Address: next.Address,
				Port:    next.Port,
				UUID:    user.ID,
				Flow:    user.Flow,
				Reality: Reality{
					ServerName:  reality.ServerName,
					PublicKey:   reality.PublicKey,
					ShortID:     reality.ShortID,
					Fingerprint: firstNonEmpty(reality.Fingerprint, ob.StreamSettings.Fingerprint, "chrome"),
				},
			}
			if err := cfg.Validate(); err != nil {
				return Config{}, fmt.Errorf("imported config is incomplete: %w", err)
			}
			return cfg, nil
		}
	}
	return Config{}, fmt.Errorf("no VLESS outbound with a user found in %s", path)
}

// xrayConfig covers only the fields worth importing. Xray's schema is enormous;
// everything else in it describes local inbounds and routing that this app
// replaces outright.
type xrayConfig struct {
	Outbounds []struct {
		Protocol string `json:"protocol"`
		Settings struct {
			VNext []struct {
				Address string `json:"address"`
				Port    uint16 `json:"port"`
				Users   []struct {
					ID   string `json:"id"`
					Flow string `json:"flow"`
				} `json:"users"`
			} `json:"vnext"`
		} `json:"settings"`
		StreamSettings struct {
			Fingerprint     string `json:"fingerprint"`
			RealitySettings struct {
				ServerName  string `json:"serverName"`
				PublicKey   string `json:"publicKey"`
				ShortID     string `json:"shortId"`
				Fingerprint string `json:"fingerprint"`
			} `json:"realitySettings"`
		} `json:"streamSettings"`
	} `json:"outbounds"`
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
