//go:build artifactdb

package main

import "sort"

var registeredFeatures []string

func registerFeature(tag string) {
	registeredFeatures = append(registeredFeatures, tag)
}

func enabledFeatures() []string {
	if len(registeredFeatures) == 0 {
		return nil
	}
	out := make([]string, len(registeredFeatures))
	copy(out, registeredFeatures)
	sort.Strings(out)
	n := 0
	for i, v := range out {
		if i == 0 || v != out[i-1] {
			out[n] = v
			n++
		}
	}
	return out[:n]
}
