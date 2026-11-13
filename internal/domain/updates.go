package domain

// Update error codes: the release check and the update button in the
// administrator's settings (change admin-update-check).
const (
	// CodeUpdateDisabled: the release check is off (UPDATE_CHECK=0) or the
	// server runs a development build, which is never compared.
	CodeUpdateDisabled = "update_check_disabled"
	// CodeUpdateFailed: the release check could not reach GitHub or got an
	// answer it does not accept.
	CodeUpdateFailed = "update_check_failed"
	// CodeUpdateUnavailable: the requested version is not the newer release
	// the last check found.
	CodeUpdateUnavailable = "update_unavailable"
	// CodeUpdateBusy: an update request is already waiting or running.
	CodeUpdateBusy = "update_in_progress"
	// CodeUpdateNoHelper: the server helper that runs updates is not
	// installed (manual installations); update with dropsite-update.
	CodeUpdateNoHelper = "update_helper_missing"
)
