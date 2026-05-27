package main

import (
	"fmt"

	"github.com/sourcehawk/triagent/internal/profile"
	"github.com/sourcehawk/triagent/pkg/auth"
	"github.com/sourcehawk/triagent/pkg/auth/kubeconfig"
	"github.com/sourcehawk/triagent/pkg/auth/teleport"
)

func newAuthProvider(profAuth profile.Auth) (auth.Provider, error) {
	switch profAuth.Kind {
	case "kubeconfig":
		return kubeconfig.NewProvider(), nil
	case "teleport":
		return teleport.NewProvider(teleport.Config{
			Proxy:         profAuth.Teleport.Proxy,
			AuthConnector: profAuth.Teleport.AuthConnector,
		}), nil
	default:
		return nil, fmt.Errorf("unknown auth.kind %q (supported: kubeconfig, teleport)", profAuth.Kind)
	}
}
