package ioctrl

import "io"

type DuplexPipe struct {
	io.ReadWriteCloser

	r *io.PipeReader
	w *io.PipeWriter
}

func (dp *DuplexPipe) Read(p []byte) (n int, err error) {
	return dp.r.Read(p)
}
func (dp *DuplexPipe) Write(p []byte) (n int, err error) {
	return dp.w.Write(p)
}
func (dp *DuplexPipe) Close() error {
	dp.r.Close()
	dp.w.Close()
	return nil
}
func NewDuplexPipe() (*DuplexPipe, *DuplexPipe) {
	r1, w1 := io.Pipe()
	r2, w2 := io.Pipe()
	return &DuplexPipe{
			r: r1,
			w: w2,
		},
		&DuplexPipe{
			r: r2,
			w: w1,
		}
}
func ProxyReadWriter(proxied io.ReadWriter) io.ReadWriteCloser {
	p1, p2 := NewDuplexPipe()

	go func() {
		_, _ = io.Copy(proxied, p1)
		p1.Close()
	}()
	go func() {
		_, _ = io.Copy(p1, proxied)
		p1.Close()
	}()

	return p2
}
