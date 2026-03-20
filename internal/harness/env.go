package harness

import "os"

func MergeEnv(extra map[string]string) []string {
	env := append([]string{}, os.Environ()...)
	for key, value := range extra {
		env = append(env, key+"="+value)
	}
	return env
}
