package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Bounds enforced on every request. Coarse guards at the API boundary; a
// provider adapter may still reject a request that passes these (e.g.
// APNs' 4KB total payload limit).
const (
	maxRequestBodyBytes     = 1 << 20 // 1 MiB
	maxTokenLength          = 4096
	maxTitleLength          = 200
	maxBodyLength           = 1000
	maxIdempotencyKeyLength = 255
	maxDataBytes            = 4096 // matches typical provider payload ceilings (e.g. APNs' 4KB total)
	maxDeviceIDsPerRequest  = 1000
)

// decodeJSON caps the request body size, rejects unknown fields, and
// writes a consistent 400 on any decode failure.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeError(w, http.StatusRequestEntityTooLarge,
				fmt.Sprintf("request body too large (max %d bytes)", maxRequestBodyBytes))
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}
