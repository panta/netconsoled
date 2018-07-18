package netconsoled

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"net"
	"time"
	"sync"
	"path"
	"strings"
)

// defaultFormat is the default format descriptor for Sinks.
const defaultFormat = "[% 15s] [% 25s | % 15f] %s"
const defaultTimeFormat = time.RFC3339

// A Sink enables storage of processed logs.
//
// Sinks may optionally implement io.Closer to flush data before the server halts.
type Sink interface {
	// Store stores a log to the Sink.
	Store(d Data) error

	// String returns the name of a Sink.
	fmt.Stringer
}

// StdoutSink creates a Sink that writes log data to stdout.
func StdoutSink() Sink {
	return newNamedSink("stdout", WriterSink(os.Stdout))
}

// MultiSink chains zero or more Sinks together.  If any Sink returns an error,
// subsequent Sinks in the chain are not invoked.
func MultiSink(sinks ...Sink) Sink {
	return &multiSink{
		sinks: sinks,
	}
}

var _ Sink = &multiSink{}

type multiSink struct {
	sinks []Sink
}

func (s *multiSink) Close() error {
	for _, sink := range s.sinks {
		// Close all sinks which implement io.Closer.
		c, ok := sink.(io.Closer)
		if !ok {
			continue
		}

		if err := c.Close(); err != nil {
			return err
		}
	}

	return nil
}

func (s *multiSink) Store(d Data) error {
	for _, sink := range s.sinks {
		if err := sink.Store(d); err != nil {
			return err
		}
	}

	return nil
}

func (s *multiSink) String() string {
	// TODO(mdlayher): loop through sinks and list.
	return "multi"
}

// FuncSink adapts a function into a Sink.
func FuncSink(store func(d Data) error) Sink {
	return &funcSink{
		fn: store,
	}
}

var _ Sink = &funcSink{}

type funcSink struct {
	fn func(d Data) error
}

func (f *funcSink) Store(d Data) error { return f.fn(d) }
func (f *funcSink) String() string     { return "func" }

// NoopSink returns a Sink that discards all logs.
func NoopSink() Sink {
	return &noopSink{}
}

var _ Sink = &noopSink{}

type noopSink struct{}

func (s *noopSink) Store(_ Data) error { return nil }
func (s *noopSink) String() string     { return "noop" }

// FileSink creates a Sink that creates or opens the specified file and appends
// logs to the file.
func FileSink(file string) (Sink, error) {
	file = filepath.Clean(file)

	// Create or open the file, and always append to it.
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}

	return newNamedSink(fmt.Sprintf("file: %q", file), WriterSink(f)), nil
}

// WriterSink creates a Sink that writes to w.
func WriterSink(w io.Writer) Sink {
	return &writerSink{
		w: w,
		// Add a newline on behalf of the caller for ease of use.
		// TODO(mdlayher): expose formatting later?
		format: defaultFormat + "\n",
		timeFormat: defaultTimeFormat,
	}
}

var _ Sink = &writerSink{}

type writerSink struct {
	w      io.Writer
	format string
	timeFormat string
}

// A syncer is a type which can flush its contents from memory to disk, e.g.
// an *os.File.
type syncer interface {
	Sync() error
}

var _ syncer = &os.File{}

func (s *writerSink) Close() error {
	// Since a writerSink can be used for files, it's possible that the io.Writer
	// has a Sync method to flush its contents to disk.  Try it first.
	if sync, ok := s.w.(syncer); ok {
		// Attempting to sync stdout, at least on Linux, results in
		// "invalid argument".  Instead of doing build tags and OS-specific
		// checks, keep it simple and just Sync as a best effort.
		_ = sync.Sync()
	}

	// Close io.Writers which also implement io.Closer.
	c, ok := s.w.(io.Closer)
	if !ok {
		return nil
	}

	return c.Close()
}

func (s *writerSink) Store(d Data) error {
	_, err := fmt.Fprintf(s.w, s.format, ipFromAddr(d.Addr), d.Received.Format(s.timeFormat), d.Log.Elapsed.Seconds(), d.Log.Message)
	return err
}

func (s *writerSink) String() string { return "writer" }

// newNamedSink wraps a Sink and replaces its name with the specified name.
// This is primarily useful for composing Sinks and providing detailed information
// to the user on startup.
func newNamedSink(name string, sink Sink) Sink {
	return &namedSink{
		sink: sink,
		name: name,
	}
}

var _ Sink = &namedSink{}

type namedSink struct {
	sink Sink
	name string
}

func (s *namedSink) Close() error {
	// Close Sinks which also implement io.Closer.
	c, ok := s.sink.(io.Closer)
	if !ok {
		return nil
	}

	return c.Close()
}
func (s *namedSink) Store(d Data) error { return s.sink.Store(d) }
func (s *namedSink) String() string     { return s.name }


// NetworkSink creates a Sink that writes to conn.
func NetworkSink(remoteAddr string) Sink {
	nw := &networkSink{
		remoteAddr: remoteAddr,
		format: defaultFormat + "\n",
		timeFormat: defaultTimeFormat,
	}
	go nw.BackgroundCommunicate()
	return nw
}

// NetworkSink creates a Sink that sends logs through the network via TCP to the
// remote node.
func NewNetworkSink(remoteAddr string) (Sink, error) {
	fmt.Fprintf(os.Stderr, "Create NetworkSink to %v\n", remoteAddr)
	return newNamedSink(fmt.Sprintf("network: %q", remoteAddr), NetworkSink(remoteAddr)), nil
}

var _ Sink = &networkSink{}

type networkSink struct {
	writerSink
	remoteAddr	string
	conn		net.Conn
	connMutex	sync.Mutex
	errCh		chan error
	dataCh		chan Data
	format		string
	timeFormat	string
}

func (s *networkSink) BackgroundCommunicate() error {
	s.errCh = make(chan error)
	s.dataCh = make(chan Data)
	for {
		if s.conn != nil {
			s.connMutex.Lock()
			s.conn.Close()
			s.conn = nil
			s.connMutex.Unlock()
		}

		fmt.Fprintf(os.Stderr, "Connecting to %v...\n", s.remoteAddr)
		conn, err := net.Dial("tcp", s.remoteAddr)
		if err != nil {
			if isEofError(err) {
				fmt.Fprintf(os.Stderr, "err = %v\n", err)
				time.Sleep(5 * time.Second)
				continue
			} else {
				return err
			}
		}
		s.connMutex.Lock()
		s.conn = conn
		s.connMutex.Unlock()
		fmt.Fprintf(os.Stderr, "Connected with %v.\n", s.remoteAddr)

		// technique inspired by https://stackoverflow.com/a/23396415/1363486
		go s.DoWrite()
		err = <- s.errCh

		if err != nil {
			if err != io.EOF {
				// serious error
				break
			}
			fmt.Fprintf(os.Stderr, "EOF -> reconnect\n")
		}
	}
	return nil
}

func (s *networkSink) DoWrite() error {
	for {
		s.connMutex.Lock()
		if s.conn == nil {
			s.connMutex.Unlock()
			time.Sleep(time.Second)
			continue
		}
		d := <-s.dataCh
		err := s.doSend(d)
		s.connMutex.Unlock()
		if err != nil {
			s.errCh <- err
			if err != io.EOF {
				return err
			}
		}
	}
}

func isEofError(err error) bool {
	// XXX TODO: check if error is compatibile with EOF (broken pipe / connection refused, ...)
	//c, ok := err.(*net.OpError)
	//if ok {
	//	fmt.Fprintf(os.Stderr, "OpError err:%v %v %v\n", c.Err, reflect.TypeOf(c.Err), reflect.TypeOf(c.Err).String())
	//}
	//sc, ok := c.Err.(*os.SyscallError)
	//if ok {
	//	fmt.Fprintf(os.Stderr, "SyscallError err:%v %v %v\n", sc.Err, reflect.TypeOf(sc.Err), reflect.TypeOf(sc.Err).String())
	//}
	//sce, ok := sc.Err.(*syscall.Errno)
	//if ok {
	//	fmt.Fprintf(os.Stderr, "SCE\n", sce)
	//}
	//fmt.Fprintf(os.Stderr, "err:%v %v %v\n", err, reflect.TypeOf(err), reflect.TypeOf(err).String())
	////return err

	return true
}

func (s *networkSink) doSend(d Data) error {
	str := fmt.Sprintf(s.format, ipFromAddr(d.Addr), d.Received.Format(s.timeFormat), d.Log.Elapsed.Seconds(), d.Log.Message)
	b := []byte(str)
	if s.conn == nil {
		return io.EOF
	}
	n, err := s.conn.Write(b)
	if err != nil {
		if isEofError(err) {
			return io.EOF
		}
		return err
	}
	if n < len(b) {
		// short write / disconnection
		return io.EOF
	}
	return nil
}

func (s *networkSink) Store(d Data) error {
	s.dataCh <- d
	return nil
}

func (s *networkSink) Close() error {
	// Since a writerSink can be used for files, it's possible that the io.Writer
	// has a Sync method to flush its contents to disk.  Try it first.
	if sync, ok := s.w.(syncer); ok {
		// Attempting to sync stdout, at least on Linux, results in
		// "invalid argument".  Instead of doing build tags and OS-specific
		// checks, keep it simple and just Sync as a best effort.
		_ = sync.Sync()
	}

	// Close io.Writers which also implement io.Closer.
	c, ok := s.w.(io.Closer)
	if !ok {
		return nil
	}

	return c.Close()
}

// --------------------------------------------------------------------------
//   Different log file per IP
// --------------------------------------------------------------------------

// FileSink creates a Sink that creates or opens the specified file and appends
// logs to the file.
func FilePerIPSink(dirname string) (Sink, error) {
	return newNamedSink(fmt.Sprintf("file-per-ip: %q", dirname), FilePerIPWriterSink(dirname)), nil
}

// WriterSink creates a Sink that writes to w.
func FilePerIPWriterSink(dirname string) Sink {
	return &filePerIPWriterSink{
		dirname: dirname,
		wm: make(map[net.Addr] io.Writer),
		// Add a newline on behalf of the caller for ease of use.
		// TODO(mdlayher): expose formatting later?
		format: defaultFormat + "\n",
		timeFormat: defaultTimeFormat,
	}
}

var _ Sink = &writerSink{}

type filePerIPWriterSink struct {
	dirname	string
	wm		map[net.Addr] io.Writer
	format	string
	timeFormat string
}

func (s *filePerIPWriterSink) Close() error {
	for _, w := range s.wm {
		// Since a writerSink can be used for files, it's possible that the io.Writer
		// has a Sync method to flush its contents to disk.  Try it first.
		if sync, ok := w.(syncer); ok {
			// Attempting to sync stdout, at least on Linux, results in
			// "invalid argument".  Instead of doing build tags and OS-specific
			// checks, keep it simple and just Sync as a best effort.
			_ = sync.Sync()
		}

		// Close io.Writers which also implement io.Closer.
		c, ok := w.(io.Closer)
		if !ok {
			return nil
		}

		err := c.Close()
		if err != nil {
			return err
		}
	}

	return nil
}

func ipFromAddr(addr net.Addr) string {
	ip_port := addr.String()
	colon := strings.LastIndex(ip_port, ":")
	if colon > 0 {
		return ip_port[0 : colon]
	}
	return ip_port
}

func (s *filePerIPWriterSink) Store(d Data) error {
	ip := ipFromAddr(d.Addr)

	w, ok := s.wm[d.Addr]
	if !ok {
		pathname := path.Join(s.dirname, ip + ".log")
		pathname = filepath.Clean(pathname)

		if _, err := os.Stat(s.dirname); os.IsNotExist(err) {
			os.Mkdir(s.dirname, 0755)
		}

		// Create or open the file, and always append to it.
		f, err := os.OpenFile(pathname, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return err
		}

		s.wm[d.Addr] = f
		w = f
	}

	_, err := fmt.Fprintf(w, s.format, ip, d.Received.Format(s.timeFormat), d.Log.Elapsed.Seconds(), d.Log.Message)
	return err
}

func (s *filePerIPWriterSink) String() string { return "filePerIPWriter" }
