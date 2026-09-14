package main

/*
#include <stdint.h>
#include <stddef.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	int (*call)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
	void (*free_buffer)(void*, size_t);
} cliproxy_host_api;

typedef struct {
	uint32_t abi_version;
	int (*call)(char*, uint8_t*, size_t, cliproxy_buffer*);
	void (*free_buffer)(void*, size_t);
	void (*shutdown)(void);
} cliproxy_plugin_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

extern int cliproxy_plugin_init(cliproxy_host_api*, cliproxy_plugin_api*);
extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static void clear_host_api(void) {
	stored_host = NULL;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}

*/
import "C"

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"github.com/tapaixx/codex-window-reset/internal/accounts"
	"github.com/tapaixx/codex-window-reset/internal/app"
	"github.com/tapaixx/codex-window-reset/internal/domain"
	"github.com/tapaixx/codex-window-reset/internal/host"
	"github.com/tapaixx/codex-window-reset/internal/management"
	"github.com/tapaixx/codex-window-reset/internal/probe"
	"github.com/tapaixx/codex-window-reset/internal/quota"
	"github.com/tapaixx/codex-window-reset/internal/store"
)

const abiVersion = 1

func main() {}

// runtimeSlot is the only mutable package-global in the Go root package. The
// lock is held for the complete duration of a dispatch so replacement and
// shutdown cannot race a management request using the old Runtime.
var runtimeSlot struct {
	sync.RWMutex
	runtime *app.Runtime
}

type hostEnvelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *hostError      `json:"error,omitempty"`
}

type hostError struct {
	Code string `json:"code,omitempty"`
}

// hostCaller adapts the C callback to host.Caller. C-owned request and
// response buffers are copied exactly once at each boundary and are never
// retained by Go.
func hostCaller(_ context.Context, operation string, request any, response any) error {
	rawRequest, err := json.Marshal(request)
	if err != nil {
		return errors.New("host request could not be encoded")
	}

	methodC := C.CString(operation)
	defer C.free(unsafe.Pointer(methodC))
	requestC := C.CBytes(rawRequest)
	if requestC == nil && len(rawRequest) != 0 {
		return errors.New("host request allocation failed")
	}
	if requestC != nil {
		defer C.free(requestC)
	}

	var requestPtr *C.uint8_t
	if requestC != nil {
		requestPtr = (*C.uint8_t)(requestC)
	}
	var responseBuffer C.cliproxy_buffer
	callCode := C.call_host_api(methodC, requestPtr, C.size_t(len(rawRequest)), &responseBuffer)
	if responseBuffer.ptr != nil {
		defer C.free_host_buffer(responseBuffer.ptr, responseBuffer.len)
	}
	if callCode != 0 {
		return errors.New("host callback failed")
	}

	rawResponse, err := copyCBuffer(responseBuffer.ptr, responseBuffer.len)
	if err != nil {
		return err
	}
	if len(rawResponse) == 0 {
		if response == nil {
			return nil
		}
		return errors.New("host callback returned no response")
	}

	var envelope hostEnvelope
	if err := json.Unmarshal(rawResponse, &envelope); err != nil || !envelope.OK {
		// Do not return host-supplied messages: an error body can contain
		// credentials or upstream response material.
		return errors.New("host callback returned an invalid response")
	}
	if response == nil || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, response); err != nil {
		return errors.New("host callback returned an invalid result")
	}
	return nil
}

func copyCBuffer(ptr unsafe.Pointer, length C.size_t) ([]byte, error) {
	if ptr == nil || length == 0 {
		return nil, nil
	}
	maxInt := uint64(^uint(0) >> 1)
	maxCInt := uint64(^uint32(0) >> 1)
	if uint64(length) > maxInt || uint64(length) > maxCInt {
		return nil, errors.New("host callback response is too large")
	}
	return C.GoBytes(ptr, C.int(length)), nil
}

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now().UTC() }

func (wallClock) AfterFunc(delay time.Duration, fn func()) domain.Timer {
	return wallTimer{timer: time.AfterFunc(delay, fn)}
}

type wallTimer struct{ timer *time.Timer }

func (t wallTimer) Stop() bool {
	if t.timer == nil {
		return false
	}
	return t.timer.Stop()
}

func newRuntime(api host.API, dataDir string) (*app.Runtime, error) {
	clock := wallClock{}
	state := store.NewRuntimeStateRepository(dataDir)
	return app.New(app.Dependencies{
		Accounts: accounts.New(api),
		Probe:    probe.New(api),
		Quota:    quota.New(api, state, clock),
		Config:   store.NewConfigRepository(dataDir),
		History:  store.NewHistoryRepository(dataDir, 100),
		State:    state,
		Clock:    clock,
	})
}

// defaultDataDir follows the host installation convention without requiring
// an environment variable or persisting a host credential in configuration.
func defaultDataDir() string {
	if info, err := os.Stat("/CLIProxyAPI/plugins"); err == nil && info.IsDir() {
		return resolveDataDir("/CLIProxyAPI/plugins")
	}
	return resolveDataDir("plugins")
}

// resolveDataDir returns a data directory that is a sibling of, not the same
// directory as, this plugin's own installed files. CLIProxyAPI's plugin
// store owns <pluginsRoot>/codex-window-reset/ as the plugin's install
// location and can recreate it on reinstall or upgrade; earlier releases
// persisted config.json, runtime-state.json, history.json, and
// reset-audit.json inside that same directory, so every plugin-store
// reinstall silently destroyed the Operator's schedule, runtime state, and
// Reset Audit history. dataDirSuffix keeps the new location deterministic
// and free of that collision. migrateLegacyDataDir moves any files an
// existing installation already wrote under the old path so upgrading to
// this fix does not itself look like data loss.
const dataDirSuffix = "-data"

func resolveDataDir(pluginsRoot string) string {
	legacyDir := filepath.Join(pluginsRoot, "codex-window-reset")
	dataDir := legacyDir + dataDirSuffix
	migrateLegacyDataDir(legacyDir, dataDir)
	return dataDir
}

var legacyDataFiles = []string{"config.json", "runtime-state.json", "history.json", "reset-audit.json"}

func migrateLegacyDataDir(legacyDir, dataDir string) {
	if legacyDir == "" || dataDir == "" || legacyDir == dataDir {
		return
	}
	if !dirHasAnyFile(legacyDir, legacyDataFiles) {
		return
	}
	if dirNonEmpty(dataDir) {
		// The new directory already has its own state (a completed prior
		// migration, or a fresh install); never overwrite it.
		return
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return
	}
	for _, name := range legacyDataFiles {
		src := filepath.Join(legacyDir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		_ = os.Rename(src, filepath.Join(dataDir, name))
	}
}

func dirHasAnyFile(dir string, names []string) bool {
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

func dirNonEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) > 0
}

//export cliproxy_plugin_init
func cliproxy_plugin_init(hostAPI *C.cliproxy_host_api, pluginAPI *C.cliproxy_plugin_api) C.int {
	if hostAPI == nil || pluginAPI == nil {
		return C.int(1)
	}
	if !validABI(uint32(hostAPI.abi_version), uint32(pluginAPI.abi_version), hostAPI.call != nil, hostAPI.free_buffer != nil) {
		return C.int(1)
	}

	runtimeSlot.Lock()
	defer runtimeSlot.Unlock()
	if runtimeSlot.runtime != nil {
		runtimeSlot.runtime.Stop()
		runtimeSlot.runtime = nil
	}

	api := host.NewClient(hostCaller)
	rt, err := newRuntime(api, defaultDataDir())
	if err != nil {
		C.clear_host_api()
		return C.int(1)
	}
	C.store_host_api(hostAPI)
	// Start performs persisted scheduler recovery and reconciliation. A new
	// installation's default config is disabled, so this remains inert.
	rt.Start()
	runtimeSlot.runtime = rt

	pluginAPI.abi_version = C.uint32_t(abiVersion)
	pluginAPI.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	pluginAPI.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	pluginAPI.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return C.int(0)
}

func validABI(hostVersion, pluginVersion uint32, hostCall, hostFree bool) bool {
	return hostVersion == abiVersion &&
		(pluginVersion == 0 || pluginVersion == abiVersion) && hostCall && hostFree
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writePluginResponse(response, mustJSON(pluginFailure("invalid_method", "method is required")))
		return C.int(1)
	}

	requestBytes := []byte("{}")
	if request != nil && requestLen > 0 {
		copied, err := copyCBuffer(unsafe.Pointer(request), requestLen)
		if err != nil {
			writePluginResponse(response, mustJSON(pluginFailure("invalid_request", "request is too large")))
			return C.int(1)
		}
		requestBytes = copied
	}

	methodName := C.GoString(method)
	result, err := dispatch(methodName, requestBytes)
	if err != nil {
		writePluginResponse(response, mustJSON(pluginFailureFromDispatch(err)))
		return C.int(1)
	}
	writePluginResponse(response, mustJSON(pluginSuccess(result)))
	return C.int(0)
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, length C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
	_ = length
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	runtimeSlot.Lock()
	if runtimeSlot.runtime != nil {
		runtimeSlot.runtime.Stop()
		runtimeSlot.runtime = nil
	}
	C.clear_host_api()
	runtimeSlot.Unlock()
}

func currentRuntime() *app.Runtime {
	runtimeSlot.RLock()
	defer runtimeSlot.RUnlock()
	return runtimeSlot.runtime
}

type pluginResponse struct {
	OK     bool             `json:"ok"`
	Result any              `json:"result,omitempty"`
	Error  *pluginErrorBody `json:"error,omitempty"`
}

type pluginErrorBody struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	Retryable     bool   `json:"retryable"`
	CorrelationID string `json:"correlation_id"`
}

func pluginSuccess(result any) pluginResponse {
	return pluginResponse{OK: true, Result: result}
}

func pluginFailure(code, message string) pluginResponse {
	return pluginFailureWith(code, message, false)
}

func pluginFailureWith(code, message string, retryable bool) pluginResponse {
	return pluginResponse{OK: false, Error: &pluginErrorBody{
		Code:          code,
		Message:       message,
		Retryable:     retryable,
		CorrelationID: newCorrelationID(),
	}}
}

func newCorrelationID() string {
	var raw [16]byte
	if _, err := cryptorand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return "request-" + strconv.FormatInt(time.Now().UTC().UnixNano(), 10)
}

type dispatchError struct {
	message   string
	retryable bool
}

func (e *dispatchError) Error() string {
	if e == nil || e.message == "" {
		return "plugin request failed"
	}
	return e.message
}

func pluginFailureFromDispatch(err error) pluginResponse {
	retryable := false
	var dispatchErr *dispatchError
	if errors.As(err, &dispatchErr) && dispatchErr != nil {
		retryable = dispatchErr.retryable
	}
	// Do not expose dispatch or host details over the native boundary. The
	// management transport still gets the same machine-readable error fields
	// as an in-router failure, including one correlation ID for this request.
	return pluginFailureWith("plugin_error", "plugin request failed", retryable)
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		return mustJSON(pluginFailureWith("encode_error", "plugin response encoding failed", false))
	}
	return raw
}

func writePluginResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.malloc(C.size_t(len(raw)))
	if ptr == nil {
		return
	}
	C.memcpy(ptr, unsafe.Pointer(&raw[0]), C.size_t(len(raw)))
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}

type pluginRegistration struct {
	SchemaVersion int                    `json:"schema_version"`
	Capabilities  pluginCapabilities     `json:"capabilities"`
	Metadata      pluginRegistrationInfo `json:"metadata"`
}

type pluginCapabilities struct {
	ManagementAPI bool `json:"management_api"`
}

type pluginRegistrationInfo struct {
	Name             string              `json:"Name"`
	Version          string              `json:"Version"`
	Author           string              `json:"Author"`
	GitHubRepository string              `json:"GitHubRepository"`
	ConfigFields     []pluginConfigField `json:"ConfigFields"`
}

type pluginConfigField struct {
	Name        string   `json:"Name"`
	Type        string   `json:"Type"`
	Description string   `json:"Description,omitempty"`
	EnumValues  []string `json:"EnumValues,omitempty"`
}

func registrationPayload() pluginRegistration {
	return pluginRegistration{
		SchemaVersion: 1,
		Capabilities:  pluginCapabilities{ManagementAPI: true},
		Metadata: pluginRegistrationInfo{
			Name:             pluginName,
			Version:          pluginVersion,
			Author:           "Codex Window Reset contributors",
			GitHubRepository: "https://github.com/tapaixx/codex-window-reset",
			ConfigFields:     []pluginConfigField{},
		},
	}
}

type abiManagementRegistration struct {
	Routes    []abiManagementRoute    `json:"routes"`
	Resources []abiManagementResource `json:"resources"`
}

type abiManagementRoute struct {
	Method string `json:"Method"`
	Path   string `json:"Path"`
}

type abiManagementResource struct {
	Path        string `json:"Path"`
	ContentType string `json:"ContentType"`
	Menu        string `json:"Menu"`
}

func managementRegistrationPayload(pluginID string) abiManagementRegistration {
	registration := management.Registration(pluginID)
	result := abiManagementRegistration{
		Routes:    make([]abiManagementRoute, 0, len(registration.Routes)),
		Resources: make([]abiManagementResource, 0, len(registration.Resources)),
	}
	for _, route := range registration.Routes {
		result.Routes = append(result.Routes, abiManagementRoute{Method: route.Method, Path: route.Path})
	}
	for _, resource := range registration.Resources {
		result.Resources = append(result.Resources, abiManagementResource{
			Path:        resource.Path,
			ContentType: resource.ContentType,
			Menu:        resource.Menu,
		})
	}
	return result
}

func managementPluginID(request []byte) string {
	var fields map[string]string
	if err := json.Unmarshal(request, &fields); err != nil {
		return management.DefaultPluginID
	}
	return management.PluginIDFromResourceFields(fields)
}

type abiHeaders map[string][]string

func (h *abiHeaders) UnmarshalJSON(data []byte) error {
	var multiple map[string][]string
	if err := json.Unmarshal(data, &multiple); err == nil {
		*h = multiple
		return nil
	}
	var scalar map[string]string
	if err := json.Unmarshal(data, &scalar); err != nil {
		return err
	}
	result := make(map[string][]string, len(scalar))
	for key, value := range scalar {
		result[key] = []string{value}
	}
	*h = result
	return nil
}

type abiBytes []byte

func (b *abiBytes) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*b = nil
		return nil
	}
	var encoded string
	if err := json.Unmarshal(data, &encoded); err == nil {
		decoded, decodeErr := base64.StdEncoding.DecodeString(encoded)
		if decodeErr == nil {
			*b = append((*b)[:0], decoded...)
			return nil
		}
		// Literal bodies make in-process host fixtures convenient while the
		// ABI's canonical representation remains base64.
		*b = append((*b)[:0], encoded...)
		return nil
	}
	var bytesValue []byte
	if err := json.Unmarshal(data, &bytesValue); err != nil {
		return err
	}
	*b = append((*b)[:0], bytesValue...)
	return nil
}

type abiManagementRequest struct {
	Method         string     `json:"method"`
	Path           string     `json:"path"`
	Headers        abiHeaders `json:"headers,omitempty"`
	Query          abiHeaders `json:"query,omitempty"`
	Body           abiBytes   `json:"body,omitempty"`
	HostCallbackID string     `json:"host_callback_id,omitempty"`
}

func (r abiManagementRequest) request() management.Request {
	return management.Request{
		Method: r.Method,
		Path:   r.Path,
		Header: map[string][]string(r.Headers),
		Body:   append([]byte(nil), r.Body...),
	}
}

type abiManagementResponse struct {
	StatusCode int                 `json:"StatusCode"`
	Headers    map[string][]string `json:"Headers,omitempty"`
	Body       []byte              `json:"Body,omitempty"`
}

func encodeManagementResponse(response management.Response) abiManagementResponse {
	headers := make(map[string][]string, len(response.Headers))
	for key, value := range response.Headers {
		headers[key] = []string{value}
	}
	return abiManagementResponse{
		StatusCode: response.StatusCode,
		Headers:    headers,
		Body:       append([]byte(nil), response.Body...),
	}
}

func dispatch(method string, request []byte) (any, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		if method == "plugin.reconfigure" {
			var payload map[string]json.RawMessage
			if err := json.Unmarshal(request, &payload); err != nil {
				return nil, errors.New("invalid reconfigure request")
			}
		}
		runtimeSlot.RLock()
		rt := runtimeSlot.runtime
		if rt == nil {
			runtimeSlot.RUnlock()
			return nil, &dispatchError{message: "plugin runtime is not initialized", retryable: true}
		}
		// Registration and reconfiguration are lifecycle notifications. Schedule
		// activation remains an explicit management PUT, preserving inert startup.
		rt.Start()
		runtimeSlot.RUnlock()
		return registrationPayload(), nil

	case "management.register":
		return managementRegistrationPayload(managementPluginID(request)), nil

	case "management.handle":
		var payload abiManagementRequest
		if err := json.Unmarshal(request, &payload); err != nil {
			return nil, errors.New("invalid management request")
		}
		runtimeSlot.RLock()
		defer runtimeSlot.RUnlock()
		if runtimeSlot.runtime == nil {
			return nil, &dispatchError{message: "plugin runtime is not initialized", retryable: true}
		}
		router := management.NewRouter(runtimeSlot.runtime, embeddedManagementAssets())
		return encodeManagementResponse(router.Handle(payload.request())), nil

	default:
		return nil, fmt.Errorf("unsupported method %q", method)
	}
}

// logOutcome is the sole root-level structured logging helper. Its argument
// list intentionally cannot carry credentials, raw identity, headers, or
// response bodies.
func logOutcome(api host.API, correlationID, accountFingerprint string, errorCode domain.ErrorCode, latencyMS int64, httpCategory string) {
	if api == nil {
		return
	}
	api.Log(context.Background(), "info", "operation outcome", map[string]any{
		"correlation_id":      correlationID,
		"account_fingerprint": accountFingerprint,
		"error_code":          errorCode,
		"latency_ms":          latencyMS,
		"http_category":       httpCategory,
	})
}
