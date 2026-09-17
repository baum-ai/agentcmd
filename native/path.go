package native

import "path"

func cleanPath(p string) string { return path.Clean(p) }
