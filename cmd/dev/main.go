// Command dev builds and runs the API and the worker together for local
// development. Run it through goir from the server directory so both
// processes restart on any .go change and share server/.env.
package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
)

type service struct {
	name  string
	pkg   string
	color string
}

var services = []service{
	{name: "api", pkg: "./cmd/api", color: "\033[35m"},
	{name: "worker", pkg: "./cmd/worker", color: "\033[33m"},
}

const reset = "\033[0m"

func main() {
	log.SetFlags(0)
	dieWithParent()
	binDir := filepath.Join("tmp", "bin")

	if err := buildAll(binDir); err != nil {
		log.Printf("\033[31m%v%s", err, reset)
		// Stay alive so goir waits for the next change instead of looping.
		waitForSignal()
		return
	}

	var procs []*exec.Cmd
	for _, s := range services {
		cmd, err := start(s, filepath.Join(binDir, s.name))
		if err != nil {
			log.Printf("\033[31m[%s] failed to start: %v%s", s.name, err, reset)
			continue
		}
		procs = append(procs, cmd)
	}

	waitForSignal()
	for _, p := range procs {
		_ = p.Process.Signal(syscall.SIGTERM)
	}
}

func buildAll(binDir string) error {
	if err := os.MkdirAll(binDir, 0o750); err != nil {
		return err
	}
	var wg sync.WaitGroup
	errs := make([]error, len(services))
	for i, s := range services {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := exec.Command("go", "build", "-o", filepath.Join(binDir, s.name), s.pkg).CombinedOutput()
			if err != nil {
				errs[i] = fmt.Errorf("[%s] build failed: %v\n%s", s.name, err, out)
			}
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func start(s service, bin string) (*exec.Cmd, error) {
	cmd := exec.Command(bin)
	cmd.SysProcAttr = childProcAttr()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	prefix := fmt.Sprintf("%s[%s]%s ", s.color, s.name, reset)
	go pipe(prefix, stdout)
	go func() {
		err := cmd.Wait()
		log.Printf("%sexited: %v", prefix, err)
	}()
	return cmd, nil
}

func pipe(prefix string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		fmt.Println(prefix + scanner.Text())
	}
}

func waitForSignal() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
}
