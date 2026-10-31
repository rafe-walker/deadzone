package model

import (
	"fmt"
	"math"
	"reflect"
)

// Sanitize walks v (a pointer to a struct) and replaces every NaN or ±Inf
// float64 it finds: pointer fields become nil, value fields become 0. It
// returns the JSON-ish paths of the fields it touched so the caller can log
// the root cause. encoding/json refuses NaN outright, which would otherwise
// take the whole state publish down with it.
func Sanitize(v any) []string {
	var hits []string
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return hits
	}
	walk(rv.Elem(), "", &hits)
	return hits
}

func bad(f float64) bool { return math.IsNaN(f) || math.IsInf(f, 0) }

func walk(rv reflect.Value, path string, hits *[]string) {
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return
		}
		if rv.Type().Elem().Kind() == reflect.Float64 {
			if bad(rv.Elem().Float()) {
				*hits = append(*hits, path)
				if rv.CanSet() {
					rv.Set(reflect.Zero(rv.Type()))
				}
			}
			return
		}
		walk(rv.Elem(), path, hits)
	case reflect.Float64:
		if bad(rv.Float()) {
			*hits = append(*hits, path)
			if rv.CanSet() {
				rv.SetFloat(0)
			}
		}
	case reflect.Struct:
		t := rv.Type()
		for i := 0; i < rv.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name := f.Tag.Get("json")
			if name == "" || name == "-" {
				name = f.Name
			} else {
				for j, c := range name {
					if c == ',' {
						name = name[:j]
						break
					}
				}
			}
			walk(rv.Field(i), join(path, name), hits)
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			walk(rv.Index(i), fmt.Sprintf("%s[%d]", path, i), hits)
		}
	case reflect.Map:
		for _, k := range rv.MapKeys() {
			mv := rv.MapIndex(k)
			if mv.Kind() == reflect.Float64 && bad(mv.Float()) {
				*hits = append(*hits, join(path, fmt.Sprint(k.Interface())))
				rv.SetMapIndex(k, reflect.ValueOf(0.0))
			}
		}
	case reflect.Interface:
		if !rv.IsNil() {
			walk(rv.Elem(), path, hits)
		}
	}
}

func join(a, b string) string {
	if a == "" {
		return b
	}
	return a + "." + b
}
