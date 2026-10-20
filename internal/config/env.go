// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package config

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/MarioUrenaGarcia/greenlens/internal/help"
)

// EnvPrefix starts the name of every configuration variable. The rest of
// the name is the path of the key in upper case, such as
// GREENLENS_AGENT_SAMPLE_INTERVAL for agent.sample_interval.
const EnvPrefix = "GREENLENS"

// EnvVar is one environment variable and the key it sets.
type EnvVar struct {
	Name string
	Key  string
}

// EnvVars lists every variable that can override the configuration. Lists
// of objects, such as backends, are set only in the file.
func EnvVars() []EnvVar {
	var out []EnvVar
	walk(reflect.ValueOf(&Config{}).Elem(), "", func(key string, v reflect.Value) {
		if !isObjectList(v.Type()) {
			out = append(out, EnvVar{Name: envName(key), Key: key})
		}
	})
	return out
}

func isObjectList(t reflect.Type) bool {
	return t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Struct
}

func envName(key string) string {
	return EnvPrefix + "_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

// ApplyEnv sets the keys whose variable is defined and not empty. Lists
// are comma separated; maps are written as key=value pairs separated by
// commas.
func ApplyEnv(cfg *Config, getenv func(string) string) error {
	var errs []error
	walk(reflect.ValueOf(cfg).Elem(), "", func(key string, v reflect.Value) {
		if isObjectList(v.Type()) {
			return
		}
		name := envName(key)
		s := strings.TrimSpace(getenv(name))
		if s == "" {
			return
		}
		if err := set(v, s); err != nil {
			errs = append(errs, help.New(help.GL3004, "name", name, "detail", err.Error()))
		}
	})
	return errors.Join(errs...)
}

// walk calls fn for every leaf field, with its dotted YAML key.
func walk(v reflect.Value, prefix string, fn func(string, reflect.Value)) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if tag == "" || tag == "-" {
			continue
		}
		key := tag
		if prefix != "" {
			key = prefix + "." + tag
		}
		fv := v.Field(i)
		if f.Type.Kind() == reflect.Struct && f.Type != reflect.TypeFor[Duration]() {
			walk(fv, key, fn)
			continue
		}
		fn(key, fv)
	}
}

var durationType = reflect.TypeFor[Duration]()

func set(v reflect.Value, s string) error {
	if v.Type() == durationType {
		d, err := time.ParseDuration(s)
		if err != nil {
			return err
		}
		v.SetInt(int64(d))
		return nil
	}
	switch v.Kind() {
	case reflect.String:
		v.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		v.SetBool(b)
	case reflect.Float64:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		v.SetFloat(f)
	case reflect.Int:
		n, err := strconv.Atoi(s)
		if err != nil {
			return err
		}
		v.SetInt(int64(n))
	case reflect.Pointer:
		if v.Type().Elem().Kind() != reflect.Float64 {
			return fmt.Errorf("unsupported type %s", v.Type())
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return err
		}
		v.Set(reflect.ValueOf(&f))
	case reflect.Slice:
		var items []string
		for _, it := range strings.Split(s, ",") {
			if it = strings.TrimSpace(it); it != "" {
				items = append(items, it)
			}
		}
		v.Set(reflect.ValueOf(items))
	case reflect.Map:
		m := map[string]string{}
		for _, pair := range strings.Split(s, ",") {
			k, val, ok := strings.Cut(strings.TrimSpace(pair), "=")
			if !ok || k == "" || val == "" {
				return fmt.Errorf("expected key=value pairs, got %q", pair)
			}
			m[k] = val
		}
		v.Set(reflect.ValueOf(m))
	default:
		return fmt.Errorf("unsupported type %s", v.Type())
	}
	return nil
}
