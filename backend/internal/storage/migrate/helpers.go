package migrate

import (
	"os"
	"time"
)

// osFS и helpers вынесены для читаемости основного файла.

type osFS struct{}

func (osFS) ReadDir(name string) ([]os.DirEntry, error) { return os.ReadDir(name) }

func osReadFile(name string) ([]byte, error) { return os.ReadFile(name) }

func timeNowUnixMilli() int64 { return time.Now().UnixMilli() }
