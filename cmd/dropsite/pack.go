package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/b0balYson/dropsite/internal/archive"
)

const (
	indexFile   = "index.html"
	packBufSize = 64 << 10
)

var errPackAborted = errors.New("upload aborted")

// checkDeployDir is the cheap local check done before any network request.
func checkDeployDir(dir string) error {
	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("cannot open directory: %w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	idx, err := os.Lstat(filepath.Join(dir, indexFile))
	if errors.Is(err, fs.ErrNotExist) {
		return withHint(fmt.Errorf("%s has no %s", dir, indexFile),
			"A site is served from index.html at the root of the directory you publish. Point deploy at the build output directory.")
	}
	if err != nil {
		return fmt.Errorf("check %s: %w", indexFile, err)
	}
	if !idx.Mode().IsRegular() {
		return fmt.Errorf("%s in %s must be a regular file (symbolic links are not published)", indexFile, dir)
	}
	return nil
}

// packer streams archive.WriteTarGz through a pipe. The archive package
// validates the whole tree before writing the first byte, so ready() reports
// every validation error before anything touches the network.
type packer struct {
	pr   *io.PipeReader
	br   *bufio.Reader
	done chan struct{}
	res  archive.Result
	err  error
}

func startPack(ctx context.Context, dir string, lim archive.Limits) *packer {
	pr, pw := io.Pipe()
	p := &packer{pr: pr, br: bufio.NewReaderSize(pr, packBufSize), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		p.res, p.err = archive.WriteTarGz(ctx, dir, pw, lim)
		_ = pw.CloseWithError(p.err)
	}()
	return p
}

// ready waits for the first archive byte or the validation error.
func (p *packer) ready() error {
	if _, err := p.br.Peek(1); err != nil {
		if perr := p.stop(); perr != nil {
			return perr
		}
		return fmt.Errorf("pack directory: %w", err)
	}
	return nil
}

// Read lets the packer be the upload body.
func (p *packer) Read(b []byte) (int, error) { return p.br.Read(b) }

// stop aborts packing (if still running), waits for it and returns its error,
// ignoring the abort itself.
func (p *packer) stop() error {
	_ = p.pr.CloseWithError(errPackAborted)
	<-p.done
	if p.err != nil && !errors.Is(p.err, errPackAborted) && !errors.Is(p.err, io.ErrClosedPipe) {
		return p.err
	}
	return nil
}
