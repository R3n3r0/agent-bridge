package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// ValidateAuth checks that the authentication mode and the upstream
// credentials in the environment are consistent.
func (c *Config) ValidateAuth(getenv func(string) string) error {
	switch c.AuthMode {
	case AuthSubscription:
		for _, k := range refusedCredentials(c.Backend) {
			if getenv(k) != "" {
				return fmt.Errorf("%s=subscription but %s is set: unset it, or use %s=api to run with an API key", EnvAuthMode, k, EnvAuthMode)
			}
		}
	case AuthAPI:
		if k := requiredCredential(c.Backend); getenv(k) == "" {
			return fmt.Errorf("%s=api requires %s to be set", EnvAuthMode, k)
		}
	case "":
		return fmt.Errorf("%s is required: set it to subscription (CLI login) or api (API key)", EnvAuthMode)
	default:
		return fmt.Errorf("%s=%q is not valid: use subscription or api", EnvAuthMode, string(c.AuthMode))
	}
	return nil
}

// ValidateNetwork refuses a non-loopback listen address unless remote access
// is explicitly allowed and a client API key is set.
func (c *Config) ValidateNetwork() error {
	if IsLoopback(c.Listen) {
		return nil
	}
	if !c.AllowRemote {
		return fmt.Errorf("listen address %s is not loopback: set allow_remote: true (or -allow-remote) to expose the server", c.Listen)
	}
	if c.APIKey == "" {
		return fmt.Errorf("listen address %s is not loopback: %s must be set when remote access is allowed", c.Listen, EnvAPIKey)
	}
	return nil
}

// IsLoopback reports whether the host part of addr is a loopback address.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func canonicalDir(p string) (string, error) {
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	p, err = filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("%s is not a directory", p)
	}
	return p, nil
}

// CanonicalDir returns the absolute, symlink-free form of an existing
// directory.
func CanonicalDir(p string) (string, error) { return canonicalDir(p) }

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}
