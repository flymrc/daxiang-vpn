package proxy

import (
	stdlog "log"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var dedicatedOutput struct {
	sync.Once
	capture *engineOutputCapture
	err     error
}

type engineOutputCapture struct {
	null, reader, writer      *os.File
	warnings, errors, unknown atomic.Uint64
	drained, stop, finished   chan struct{}
	loggingOnce, finishOnce   sync.Once
}

// rawLevelDecoder retains no characters, message strings, or complete lines.
// It recognizes only the forced sing-box no-color/non-timestamp WARN[/ERROR[
// prefixes. All other bytes, including invalid UTF8, ANSI and arbitrarily long
// or multiline messages, become an anonymous suppressed-line count.
type rawLevelDecoder struct {
	position                     uint8
	warn, fail, recognized, seen bool
}

func (d *rawLevelDecoder) reset() { *d = rawLevelDecoder{warn: true, fail: true} }
func (d *rawLevelDecoder) consume(c *engineOutputCapture, data []byte) {
	for _, b := range data {
		if b == '\n' {
			if d.seen && !d.recognized {
				c.unknown.Add(1)
			}
			d.reset()
			continue
		}
		d.seen = true
		if d.position >= 6 {
			continue
		}
		position := int(d.position)
		if position >= len("WARN[") || b != "WARN["[position] {
			d.warn = false
		}
		if position >= len("ERROR[") || b != "ERROR["[position] {
			d.fail = false
		}
		d.position++
		if d.warn && d.position == 5 {
			c.warnings.Add(1)
			d.recognized = true
		}
		if d.fail && d.position == 6 {
			c.errors.Add(1)
			d.recognized = true
		}
	}
}

// suppressDedicatedOutput belongs only to the hidden, disposable child.
// Dependency output is untrusted even when split across writes or lines. It
// never enters a file, formatter, or redactor. Keep the sink installed until
// process exit, including library goroutines that finish after engine Close.
// General RunEngine calls must never invoke this process-wide operation.
func suppressDedicatedOutput() (*engineOutputCapture, error) {
	dedicatedOutput.Do(func() {
		oldStdout, oldStderr := os.Stdout, os.Stderr
		file, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
		if err != nil {
			dedicatedOutput.err = err
			return
		}
		reader, writer, err := os.Pipe()
		if err != nil {
			_ = file.Close()
			dedicatedOutput.err = err
			return
		}
		capture := &engineOutputCapture{null: file, reader: reader, writer: writer, drained: make(chan struct{}), stop: make(chan struct{}), finished: make(chan struct{})}
		// Start the fixed reader before routing any output to its pipe. The
		// reader never waits for disk I/O and continues after logger failure.
		go capture.drain()
		dedicatedOutput.capture = capture
		if err := suppressNativeOutput(file, writer); err != nil {
			// A partial native redirection must retain its sink as well.
			dedicatedOutput.err = err
			_ = suppressNativeOutput(file, file)
			os.Stdout, os.Stderr = file, file
			_ = writer.Close()
			return
		}
		// Go's standard logger and third-party log.New instances may cache the
		// original os.File object. Windows SetStdHandle does not retarget those
		// handles; retire them only in this disposable dedicated process.
		if err := retireCachedStandardOutput(oldStdout, oldStderr, file, writer); err != nil {
			dedicatedOutput.err = err
			_ = suppressNativeOutput(file, file)
			os.Stdout, os.Stderr = file, file
			stdlog.SetOutput(file)
			_ = writer.Close()
			return
		}
		os.Stdout, os.Stderr = file, writer
		stdlog.SetOutput(writer)
	})
	return dedicatedOutput.capture, dedicatedOutput.err
}

func (c *engineOutputCapture) drain() {
	defer close(c.drained)
	defer c.reader.Close()
	var decoder rawLevelDecoder
	decoder.reset()
	var buffer [4096]byte
	for {
		n, err := c.reader.Read(buffer[:])
		if n > 0 {
			decoder.consume(c, buffer[:n])
			clear(buffer[:n])
		}
		if err != nil {
			if decoder.seen && !decoder.recognized {
				c.unknown.Add(1)
			}
			return
		}
	}
}

func (c *engineOutputCapture) flush(recorder *engineLogRecorder) {
	for _, category := range []struct {
		counter *atomic.Uint64
		event   EngineLogEventKind
		code    EngineLogCode
	}{
		{&c.warnings, EngineLogDependencyWarning, EngineLogCodeDependencyWarning},
		{&c.errors, EngineLogDependencyError, EngineLogCodeDependencyError},
		{&c.unknown, EngineLogRawOutputSuppressed, EngineLogCodeRawOutputSuppressed},
	} {
		count := category.counter.Swap(0)
		if count > 0 {
			if count > engineLogMaxSequence {
				count = engineLogMaxSequence
			}
			_ = recorder.append(EngineLogEvent{Event: category.event, Code: category.code, Count: count})
		}
	}
}

func (c *engineOutputCapture) startLog(recorder *engineLogRecorder) {
	c.loggingOnce.Do(func() {
		go func() {
			defer close(c.finished)
			ticker := time.NewTicker(500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					c.flush(recorder)
				case <-c.stop:
					c.flush(recorder)
					return
				}
			}
		}()
	})
}

func (c *engineOutputCapture) finish(recorder *engineLogRecorder) {
	c.finishOnce.Do(func() {
		// Return standard handles to the permanent null sink, never the parent
		// console. This also discards goroutines that finish after Close.
		_ = suppressNativeOutput(c.null, c.null)
		os.Stdout, os.Stderr = c.null, c.null
		stdlog.SetOutput(c.null)
		_ = c.writer.Close()
		select {
		case <-c.drained:
		case <-time.After(250 * time.Millisecond):
			_ = c.reader.Close()
			recorder.log.MarkDegraded(EngineLogCodeShutdown)
		}
		c.startLog(recorder)
		close(c.stop)
		<-c.finished
	})
}
