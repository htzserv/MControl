package api

import (
	"net/http"
	"testing"
)

func TestRegisterRoutesNoPanic(t *testing.T) {
	mux := http.NewServeMux()
	h := &Handler{limiter: newLimiter()}
	h.Register(mux)
}
