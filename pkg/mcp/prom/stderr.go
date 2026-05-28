package prom

import (
	"io"
	"os"
)

func osStderr() io.Writer { return os.Stderr }
