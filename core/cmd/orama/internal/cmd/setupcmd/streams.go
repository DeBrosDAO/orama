package setupcmd

import (
	"io"
	"os"
	"sync"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
)

// filterStreams points os.Stdout and os.Stderr at pipes whose contents reach the
// real streams through the terminal filter (setup.NewTerminalWriter). The code
// setup reuses from `orama node setup` prints with fmt and hands the streams to
// ssh and scp, and what a machine says to those would otherwise reach the
// operator's terminal as it is. Bytes are passed on as they arrive, so a prompt
// without a newline is shown at once. The returned func puts the streams back
// and waits for the pipes to drain.
func filterStreams() (restore func(), err error) {
	realOut, realErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		_ = outR.Close()
		_ = outW.Close()
		return nil, err
	}
	os.Stdout, os.Stderr = outW, errW
	var wg sync.WaitGroup
	forward := func(dst io.Writer, src *os.File) {
		defer wg.Done()
		_, _ = io.Copy(setup.NewTerminalWriter(dst), src)
	}
	wg.Add(2)
	go forward(realOut, outR)
	go forward(realErr, errR)
	return func() {
		os.Stdout, os.Stderr = realOut, realErr
		_ = outW.Close()
		_ = errW.Close()
		wg.Wait()
		_ = outR.Close()
		_ = errR.Close()
	}, nil
}
