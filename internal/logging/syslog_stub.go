//go:build windows

package logging

import (
	"fmt"
	"io"
)

func openSyslog() (io.Writer, func() error, error) {
	return nil, nil, fmt.Errorf("syslog logging is not supported on windows")
}
