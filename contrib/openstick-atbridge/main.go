// atbridge exposes a WWAN AT character device that only answers a blocking
// read as a standard PTY that ordinary serial libraries can use.
//
// The UFI003 / OpenStick 410 bam-dmux port never raises POLLIN and returns
// EAGAIN for a non-blocking read, so tools that poll (socat, the original
// VoCat transport) see an idle port even while the modem is answering. This
// bridge keeps exactly one blocking read outstanding on the device and
// forwards whatever arrives into a PTY master, which behaves like a normal
// serial port for consumers.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/sys/unix"
)

func main() {
	dev := flag.String("dev", "/dev/wwan0at0", "WWAN AT character device")
	link := flag.String("link", "/dev/vocat-at", "PTY symlink to expose")
	flag.Parse()

	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	devFD, err := unix.Open(*dev, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		log.Fatalf("open %s: %v", *dev, err)
	}
	defer unix.Close(devFD)
	log.Printf("opened %s (fd %d)", *dev, devFD)

	master, err := unix.Open("/dev/ptmx", unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		log.Fatalf("open ptmx: %v", err)
	}
	defer unix.Close(master)

	if err := unix.IoctlSetPointerInt(master, unix.TIOCSPTLCK, 0); err != nil {
		log.Fatalf("unlock pty: %v", err)
	}
	number, err := unix.IoctlGetInt(master, unix.TIOCGPTN)
	if err != nil {
		log.Fatalf("get pty number: %v", err)
	}
	slavePath := fmt.Sprintf("/dev/pts/%d", number)
	if err := os.Remove(*link); err != nil && !os.IsNotExist(err) {
		log.Fatalf("remove stale link: %v", err)
	}
	if err := os.Symlink(slavePath, *link); err != nil {
		log.Fatalf("link %s -> %s: %v", *link, slavePath, err)
	}
	log.Printf("exported %s -> %s", *link, slavePath)

	// Keep the slave open ourselves so the PTY survives a consumer disconnect
	// and the driver read stays valid across consumer restarts.
	slave, err := unix.Open(slavePath, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		log.Fatalf("open slave %s: %v", slavePath, err)
	}
	defer unix.Close(slave)
	if err := unix.SetNonblock(master, true); err != nil {
		log.Printf("set master nonblocking: %v", err)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		log.Printf("shutting down")
		_ = os.Remove(*link)
		os.Exit(0)
	}()

	// device -> pty: one long-lived blocking read. This is the only read shape
	// the bam-dmux AT port answers, and it must never be torn down.
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := unix.Read(devFD, buf)
			if err == unix.EINTR {
				continue
			}
			if err == unix.EAGAIN {
				continue
			}
			if err != nil {
				log.Printf("device read: %v", err)
				return
			}
			if n == 0 {
				continue
			}
			if _, werr := unix.Write(master, buf[:n]); werr != nil && werr != unix.EAGAIN {
				log.Printf("pty write: %v", werr)
			}
		}
	}()

	// pty -> device: forward consumer commands, retrying on a full device
	// buffer instead of dropping bytes.
	buf := make([]byte, 1024)
	for {
		n, err := unix.Read(master, buf)
		if err == unix.EINTR || err == unix.EAGAIN {
			pollReadable(master)
			continue
		}
		if err != nil {
			log.Printf("pty read: %v", err)
			return
		}
		if n == 0 {
			continue
		}
		written := 0
		for written < n {
			count, werr := unix.Write(devFD, buf[written:n])
			if werr == unix.EINTR {
				continue
			}
			if werr == unix.EAGAIN {
				pollWritable(devFD)
				continue
			}
			if werr != nil {
				log.Printf("device write: %v", werr)
				break
			}
			written += count
		}
	}
}

func pollReadable(fd int) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	_, _ = unix.Poll(fds, 100)
}

func pollWritable(fd int) {
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
	_, _ = unix.Poll(fds, 100)
}
