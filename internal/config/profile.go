package config

import (
	"fmt"
	"strings"
	"time"
)

type Profile string

const (
	ProfileGeneric    Profile = "generic"
	ProfileKubernetes Profile = "kubernetes"
)

func ParseProfile(value string) (Profile, error) {
	profile := Profile(strings.ToLower(strings.TrimSpace(value)))
	if profile == "" {
		profile = ProfileGeneric
	}
	switch profile {
	case ProfileGeneric, ProfileKubernetes:
		return profile, nil
	default:
		return "", fmt.Errorf("profile must be generic or kubernetes")
	}
}

func DefaultsForProfile(profile Profile) (Config, error) {
	parsed, err := ParseProfile(string(profile))
	if err != nil {
		return Config{}, err
	}
	cfg := Default()
	if parsed == ProfileKubernetes {
		cfg.Shutdown.Deadline = NewDuration(30 * time.Second)
	}
	return cfg, nil
}
