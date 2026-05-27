// Vendored and adapted from an internal SDK (teleport/auth/auth.go).
// Package renamed to sdk for use in github.com/sourcehawk/triagent.

// Package sdk provides Teleport SSO authentication and Kubernetes cluster
// access helpers, vendored from c1-sdk/teleport/{auth,k8s}.
package sdk

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gravitational/teleport/api/profile"
)

// Teleport connection defaults. Callers that have their own proxy/connector
// should pass them via Config (see NewProvider).
const (
	// DefaultProxyAddr is the Teleport proxy address used for login.
	// Override via NewProviderWithConfig for your own Teleport deployment.
	DefaultProxyAddr = ""
	// DefaultAuthConnector is the SSO connector name.
	DefaultAuthConnector = "okta"
)

// ProfileLoader loads a tsh profile from disk.
type ProfileLoader interface {
	FromDir(dir, name string) (*profile.Profile, error)
}

// TshRunner executes the tsh binary for SSO login.
type TshRunner interface {
	Run(tshPath string) error
}

// BinaryFinder locates executables on the filesystem.
type BinaryFinder interface {
	UserHomeDir() (string, error)
	Stat(name string) (os.FileInfo, error)
	LookPath(file string) (string, error)
}

// ExpiryChecker determines whether a profile session has expired.
type ExpiryChecker interface {
	IsExpired(prof *profile.Profile) bool
}

// Auth holds injectable dependencies for Teleport authentication.
type Auth struct {
	// Profile loads tsh profiles from disk.
	Profile ProfileLoader
	// Tsh executes the tsh binary for SSO login.
	Tsh TshRunner
	// Binary locates executables on the filesystem.
	Binary BinaryFinder
	// Expiry checks whether a profile session has expired.
	Expiry ExpiryChecker
}

// defaultProfileLoader uses the real teleport profile package.
type defaultProfileLoader struct{}

func (defaultProfileLoader) FromDir(dir, name string) (*profile.Profile, error) {
	return profile.FromDir(dir, name)
}

// defaultTshRunner shells out to the real tsh binary.
type defaultTshRunner struct {
	proxy     string
	connector string
}

func (r defaultTshRunner) Run(tshPath string) error {
	tshCmd := exec.Command(tshPath, "login", "--proxy="+r.proxy, "--auth="+r.connector)
	tshCmd.Stdin = os.Stdin
	tshCmd.Stdout = os.Stderr
	tshCmd.Stderr = os.Stderr
	return tshCmd.Run()
}

// defaultBinaryFinder uses real OS calls.
type defaultBinaryFinder struct{}

func (defaultBinaryFinder) UserHomeDir() (string, error)          { return os.UserHomeDir() }
func (defaultBinaryFinder) Stat(name string) (os.FileInfo, error) { return os.Stat(name) }
func (defaultBinaryFinder) LookPath(file string) (string, error)  { return exec.LookPath(file) }

// defaultExpiryChecker checks expiry using the real TLS certificate on disk.
type defaultExpiryChecker struct{}

func (defaultExpiryChecker) IsExpired(prof *profile.Profile) bool {
	expiry, ok := prof.Expiry()
	return ok && time.Now().After(expiry)
}

// DefaultAuth is the package-level default instance used by the public functions.
var DefaultAuth = &Auth{
	Profile: defaultProfileLoader{},
	Tsh:     defaultTshRunner{proxy: DefaultProxyAddr, connector: DefaultAuthConnector},
	Binary:  defaultBinaryFinder{},
	Expiry:  defaultExpiryChecker{},
}

// newAuth constructs an Auth instance with the given proxy and connector.
func newAuth(proxy, connector string) *Auth {
	return &Auth{
		Profile: defaultProfileLoader{},
		Tsh:     defaultTshRunner{proxy: proxy, connector: connector},
		Binary:  defaultBinaryFinder{},
		Expiry:  defaultExpiryChecker{},
	}
}

// Login performs a Teleport SSO login by shelling out to tsh.
func Login() error {
	return DefaultAuth.Login()
}

// Login performs a Teleport SSO login using the injected dependencies.
func (a *Auth) Login() error {
	tshPath, err := a.FindTshBinary()
	if err != nil {
		return err
	}

	if err := a.Tsh.Run(tshPath); err != nil {
		return fmt.Errorf("tsh login failed: %w", err)
	}

	log.Info("Teleport login successful")
	return nil
}

// EnsureLoggedIn checks that a valid tsh profile exists and the session
// has not expired. If no session exists or it has expired, it automatically
// triggers a Teleport SSO login. Returns the loaded profile.
func EnsureLoggedIn() (*profile.Profile, error) {
	return DefaultAuth.EnsureLoggedIn()
}

// IsAuthenticated reports whether a valid, non-expired Teleport session exists
// without triggering a login. Safe to call from a UI goroutine.
func IsAuthenticated() bool {
	return DefaultAuth.IsAuthenticated()
}

// IsAuthenticated reports whether a valid, non-expired session exists.
func (a *Auth) IsAuthenticated() bool {
	prof, err := a.Profile.FromDir("", "")
	if err != nil {
		return false
	}
	return !a.Expiry.IsExpired(prof)
}

// EnsureLoggedIn checks the profile and re-authenticates if needed.
func (a *Auth) EnsureLoggedIn() (*profile.Profile, error) {
	prof, err := a.Profile.FromDir("", "")
	if err != nil {
		log.Info("No Teleport session found, logging in...")
		if err := a.Login(); err != nil {
			return nil, err
		}
		prof, err = a.Profile.FromDir("", "")
		if err != nil {
			return nil, fmt.Errorf("loading profile after login: %w", err)
		}
		return prof, nil
	}

	if a.Expiry.IsExpired(prof) {
		log.Info("Teleport session expired, re-authenticating...")
		if err := a.Login(); err != nil {
			return nil, err
		}
		prof, err = a.Profile.FromDir("", "")
		if err != nil {
			return nil, fmt.Errorf("loading profile after login: %w", err)
		}
	}

	return prof, nil
}

// FindTshBinary locates the tsh binary, checking ~/.tsh/bin/tsh first
// (installed by Teleport auto-updater), then falling back to PATH.
func FindTshBinary() (string, error) {
	return DefaultAuth.FindTshBinary()
}

// FindTshBinary locates the tsh binary using the injected dependencies.
func (a *Auth) FindTshBinary() (string, error) {
	home, err := a.Binary.UserHomeDir()
	if err == nil {
		tshBin := filepath.Join(home, ".tsh", "bin", "tsh")
		if _, err := a.Binary.Stat(tshBin); err == nil {
			return tshBin, nil
		}
	}

	tshPath, err := a.Binary.LookPath("tsh")
	if err != nil {
		return "", fmt.Errorf("tsh binary not found\n\nInstall tsh: https://goteleport.com/docs/installation/")
	}
	return tshPath, nil
}
