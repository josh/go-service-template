package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

type addrs []string

func (a *addrs) String() string     { return "" }
func (a *addrs) Set(v string) error { *a = append(*a, v); return nil }

func parseListenAddr(addr string) (network, address string, err error) {
	u, err := url.Parse(addr)
	if err != nil {
		return "", "", fmt.Errorf("invalid address format: %w", err)
	}

	switch u.Scheme {
	case "unix":
		path := u.Path
		if u.Host != "" {
			path = u.Host + path
		}
		if path == "" {
			return "", "", fmt.Errorf("unix socket path cannot be empty")
		}
		return "unix", path, nil
	case "":
		return "tcp", addr, nil
	default:
		return "", "", fmt.Errorf("unsupported scheme %q (supported: unix)", u.Scheme)
	}
}

type config struct {
	addresses     []string
	listenPid     int
	listenFds     int
	listenFdnames []string
}

func parseConfig() (*config, error) {
	cfg := &config{}
	flag.Var((*addrs)(&cfg.addresses), "listen", "address to listen on (TCP: :8080, 127.0.0.1:80; Unix: unix:///run/foo.sock)")
	flag.Parse()

	if pid := os.Getenv("LISTEN_PID"); pid != "" {
		var err error
		cfg.listenPid, err = strconv.Atoi(pid)
		if err != nil {
			return nil, fmt.Errorf("invalid LISTEN_PID %q: %w", pid, err)
		}
		if err := os.Unsetenv("LISTEN_PID"); err != nil {
			return nil, fmt.Errorf("failed to unset LISTEN_PID: %w", err)
		}
	}
	if fds := os.Getenv("LISTEN_FDS"); fds != "" {
		var err error
		cfg.listenFds, err = strconv.Atoi(fds)
		if err != nil {
			return nil, fmt.Errorf("invalid LISTEN_FDS %q: %w", fds, err)
		}
		if err := os.Unsetenv("LISTEN_FDS"); err != nil {
			return nil, fmt.Errorf("failed to unset LISTEN_FDS: %w", err)
		}
	}
	if fdnames := os.Getenv("LISTEN_FDNAMES"); fdnames != "" {
		cfg.listenFdnames = strings.Split(fdnames, ":")
		if err := os.Unsetenv("LISTEN_FDNAMES"); err != nil {
			return nil, fmt.Errorf("failed to unset LISTEN_FDNAMES: %w", err)
		}
	}

	return cfg, nil
}

const listenFdsStart = 3

func (c *config) listeners() ([]net.Listener, error) {
	var listeners []net.Listener
	var err error

	defer func() {
		if err != nil {
			for _, l := range listeners {
				_ = l.Close()
			}
		}
	}()

	if c.listenPid != 0 && c.listenPid == os.Getpid() {
		for i := 0; i < c.listenFds; i++ {
			fd := listenFdsStart + i
			syscall.CloseOnExec(fd)
			name := "LISTEN_FD_" + strconv.Itoa(fd)
			if i < len(c.listenFdnames) && len(c.listenFdnames[i]) > 0 {
				name = c.listenFdnames[i]
			}
			f := os.NewFile(uintptr(fd), name)
			if l, ferr := net.FileListener(f); ferr == nil {
				listeners = append(listeners, l)
				if ferr := f.Close(); ferr != nil {
					err = fmt.Errorf("failed to close file %s: %w", f.Name(), ferr)
					return nil, err
				}
			}
		}
	}

	for _, addr := range c.addresses {
		network, address, parseErr := parseListenAddr(addr)
		if parseErr != nil {
			err = fmt.Errorf("invalid listen address %q: %w", addr, parseErr)
			return nil, err
		}

		var l net.Listener
		l, err = net.Listen(network, address)
		if err != nil {
			err = fmt.Errorf("failed to listen on %s (%s): %w", addr, network, err)
			return nil, err
		}
		listeners = append(listeners, l)
	}

	return listeners, nil
}

func main() {
	cfg, err := parseConfig()
	if err != nil {
		slog.Error("config error", "error", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Info("request", "method", r.Method, "path", r.URL.Path, "remote", r.RemoteAddr)
		if _, err := w.Write([]byte("Hello, World!")); err != nil {
			slog.Warn("write failed", "error", err)
		}
	})

	listeners, err := cfg.listeners()
	if err != nil {
		slog.Error("failed to get listeners", "error", err)
		os.Exit(1)
	}

	if len(listeners) == 0 {
		slog.Error("no listeners configured")
		os.Exit(1)
	}

	srv := &http.Server{Handler: handler}

	go func() {
		<-ctx.Done()
		if err := srv.Shutdown(context.Background()); err != nil {
			slog.Error("shutdown failed", "error", err)
		}
	}()

	var wg sync.WaitGroup
	for _, l := range listeners {
		slog.Info("listener", "addr", l.Addr().String())
		wg.Add(1)
		go func(listener net.Listener) {
			defer wg.Done()
			if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
				slog.Error("server stopped", "addr", listener.Addr().String(), "error", err)
			}
		}(l)
	}

	<-ctx.Done()
	wg.Wait()
}
