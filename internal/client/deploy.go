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
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() { _ = pw.CloseWithError(writeMultipart(mw, archive, progress)) }()

	req, err := c.newRequest(ctx, http.MethodPost, refPath("/sites", ref)+"/deploys", nil, pr)
	if err != nil {
		_ = pr.CloseWithError(err)
		return nil, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	var out DeployResult
	err = c.send(req, &out)
	// Unblock the writer goroutine if the server answered before reading everything.
	_ = pr.CloseWithError(io.ErrClosedPipe)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func writeMultipart(mw *multipart.Writer, archive io.Reader, progress ProgressFunc) error {
	part, err := mw.CreateFormFile(archiveField, archiveFilename)
	if err != nil {
		return fmt.Errorf("start multipart: %w", err)
	}
	var dst io.Writer = part
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
