package modem

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

type SerialOpener struct {
	SessionOptions SessionOptions
	BaudRate       int
}

func (opener SerialOpener) Open(ctx context.Context, port Port) (Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := port.OpenPath()
	if path == "" {
		return nil, errors.New("modem: candidate has no AT port")
	}
	// Some downstream WWAN drivers (UFI003 / OpenStick 410 bam-dmux) only answer
	// a read that is already blocked in the driver, which breaks the poll plus
	// non-blocking model every serial consumer uses. When a bridge exposes the
	// same port as a PTY, prefer the PTY: it behaves like an ordinary serial
	// port while the bridge keeps the blocking read outstanding.
	//
	// The decision has to happen before isNativeWWANATPath, because the bridged
	// PTY must take the ordinary serial path, not the WWAN character-device one.
	useNativeWWAN := isNativeWWANATPath(path)
	if useNativeWWAN {
		if bridged := bridgedATPath(path); bridged != "" {
			path = bridged
			useNativeWWAN = false
		}
	}
	release, ok := tryAcquireATPort(path)
	if !ok {
		return nil, fmt.Errorf("open AT port %s: %w", path, ErrATPortBusy)
	}
	leaseTransferred := false
	defer func() {
		if !leaseTransferred {
			release()
		}
	}()

	if useNativeWWAN {
		rawPort, err := openNativeWWANATTransport(path)
		if err != nil {
			return nil, fmt.Errorf("open WWAN AT port %s: %w", path, err)
		}
		if err := rawPort.ResetInputBuffer(); err != nil {
			_ = rawPort.Close()
			return nil, fmt.Errorf("reset WWAN AT input buffer %s: %w", path, err)
		}
		session, err := NewSession(rawPort, opener.SessionOptions)
		if err != nil {
			_ = rawPort.Close()
			return nil, err
		}
		session.setCloseHook(release)
		leaseTransferred = true
		return session, nil
	}
	baudRate := opener.BaudRate
	if baudRate <= 0 {
		baudRate = 115200
	}
	rawPort, err := openSerialTransport(path, baudRate)
	if err != nil {
		return nil, fmt.Errorf("open AT port %s: %w", path, err)
	}
	if err := rawPort.ResetInputBuffer(); err != nil {
		_ = rawPort.Close()
		return nil, fmt.Errorf("reset AT input buffer %s: %w", path, err)
	}
	session, err := NewSession(rawPort, opener.SessionOptions)
	if err != nil {
		_ = rawPort.Close()
		return nil, err
	}
	session.setCloseHook(release)
	leaseTransferred = true
	return session, nil
}

func isNativeWWANATPath(path string) bool {
	_, kind, _, ok := parseWWANPortName(filepath.Base(filepath.Clean(path)))
	return ok && kind == "at"
}
