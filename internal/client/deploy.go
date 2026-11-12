package client

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
)

const (
	archiveField    = "archive"
	archiveFilename = "site.tar.gz"
)

// ProgressFunc receives the total number of archive bytes sent so far.
type ProgressFunc func(sent int64)

// Deploy uploads archive (a tar.gz or zip stream) as a new release of ref.
// The body is streamed as multipart/form-data without buffering the archive.
// Only ctx and the response-header timeout bound the call, so large uploads
// over slow links are not cut off. progress may be nil.
func (c *Client) Deploy(ctx context.Context, ref string, archive io.Reader, progress ProgressFunc) (*DeployResult, error) {
	return c.DeployWith(ctx, ref, archive, progress, DeployOptions{})
}

// DeployWith is Deploy with options (a draft instead of going live).
func (c *Client) DeployWith(ctx context.Context, ref string, archive io.Reader, progress ProgressFunc,
	opts DeployOptions) (*DeployResult, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = pw.CloseWithError(writeMultipart(mw, archive, progress))
	}()
	// Unblock and await the writer even if the server answered before reading
	// everything, so archive is no longer in use once Deploy returns.
	defer func() {
		_ = pr.CloseWithError(io.ErrClosedPipe)
		<-done
	}()

	req, err := c.newRequest(ctx, http.MethodPost, sitePath(ref)+"/deploys", opts.query(), pr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var out DeployResult
	if err := c.send(req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func writeMultipart(mw *multipart.Writer, archive io.Reader, progress ProgressFunc) error {
	part, err := mw.CreateFormFile(archiveField, archiveFilename)
	if err != nil {
		return fmt.Errorf("start multipart: %w", err)
	}
	dst := part
	if progress != nil {
		dst = &progressWriter{w: part, fn: progress}
	}
	if _, err := io.Copy(dst, archive); err != nil {
		return fmt.Errorf("stream archive: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("finish multipart: %w", err)
	}
	return nil
}

type progressWriter struct {
	w    io.Writer
	fn   ProgressFunc
	sent int64
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.sent += int64(n)
	p.fn(p.sent)
	return n, err
}
