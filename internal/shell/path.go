package shell

import "path"

func cleanPath(p string) string { return path.Clean(p) }
