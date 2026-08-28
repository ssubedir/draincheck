package config

import (
	"fmt"
	"time"
)

// Duration is a human-readable duration in configuration files.
type Duration string

func NewDuration(value time.Duration) Duration {
	return Duration(value.String())
}

func (d Duration) Value() time.Duration {
	value, _ := time.ParseDuration(string(d))
	return value
}

func (d Duration) IsZero() bool {
	return d == ""
}

func (d *Duration) UnmarshalText(text []byte) error {
	value, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", text, err)
	}
	if value < 0 {
		return fmt.Errorf("duration must not be negative")
	}
	*d = Duration(value.String())
	return nil
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(d), nil
}
