package mcp

import "strings"

// UploadCommand is the shell command that sends a folder to an upload link
// as one tar stream. curl prints the server's JSON answer either way (no
// -f), so the assistant reads what was received or what to fix.
func UploadCommand(uploadURL string) string {
	return "tar -C <folder> -cf - . | curl -sS --data-binary @- -H 'Content-Type: application/x-tar' '" +
		strings.ReplaceAll(uploadURL, "'", "") + "'"
}

// UploadNextSteps tells the assistant how to use the upload link (spec
// upload-link): any assistant must get it right without other context.
const UploadNextSteps = "Send the files through upload_url, not through the chat. " +
	"If you can run shell commands on the computer that has the site files, run upload_command with <folder> " +
	"replaced by the site folder (the one with index.html) and read its JSON answer (received, skipped, or an " +
	"error to fix). If you cannot, give the user upload_url and say in plain words: \"Open this link, drag the " +
	"site folder (or the files) onto the page, and when it says Done, come back and write: done\"; then wait for " +
	"their reply. After that call commit_upload with upload_id. The link works for one hour from its last use and " +
	"only for this upload; for a new link call begin_upload again. Never use tunnels (ngrok and the like), file " +
	"hosting or other servers to move the files. upload_files stays available for small files or fetch from " +
	"public addresses."

// withUploadLink adds the command and the instructions to a begin_upload
// result that carries an upload link.
func withUploadLink(u Upload) Upload {
	if u.UploadURL == "" {
		return u
	}
	u.UploadCommand = UploadCommand(u.UploadURL)
	u.NextSteps = UploadNextSteps
	return u
}
