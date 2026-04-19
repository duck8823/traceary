package cli

import "os"

// Local helpers used by read_color_test.go to augment t.Setenv with a true
// "unset" operation. Kept in a separate file so the test file stays focused
// on the actual assertions.

func lookupEnv(key string) (string, bool) { return os.LookupEnv(key) }
func unsetEnv(key string)                 { _ = os.Unsetenv(key) }
func setEnvVar(key, value string)         { _ = os.Setenv(key, value) }
