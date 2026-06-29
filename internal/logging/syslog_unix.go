//go:build !windows

package logging

import (
	"io"
	"log/syslog"
)

func openSyslog() (io.Writer, func() error, error) {
	w, err := syslog.New(syslog.LOG_INFO|syslog.LOG_USER, "icestream")
	if err != nil {
		return nil, nil, err
	}
	return w, w.Close, nil
}
