package starlink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang/protobuf/jsonpb" //nolint:staticcheck // protoreflect's dynamic messages marshal through jsonpb
	"github.com/jhump/protoreflect/desc"
	"github.com/jhump/protoreflect/dynamic"
	"github.com/jhump/protoreflect/dynamic/grpcdynamic"
	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// Caller sends one request to SpaceX.API.Device.Device/Handle and returns
// the response as JSON with original (snake_case) field names. Tests use a
// fixture-backed implementation; production uses ReflectCaller.
type Caller interface {
	Call(ctx context.Context, request map[string]any) ([]byte, error)
	Close() error
}

const (
	// DefaultAddr is the dish's fixed local address (also reachable in
	// bypass mode with a static route).
	DefaultAddr = "192.168.100.1:9200"
	serviceName = "SpaceX.API.Device.Device"
	methodName  = "Handle"
)

// ErrReflection wraps failures of the gRPC reflection handshake. The source
// reports these as "uplink unknown" rather than treating the dish as down:
// the HA integration currently fails on new dish firmware with
// "proto: not found" for this exact reason, and we do not want a schema
// problem to look like an outage.
var ErrReflection = errors.New("starlink: gRPC reflection failed")

// ErrUnreachable wraps transport-level failures (dish off, no route).
var ErrUnreachable = errors.New("starlink: dish unreachable")

// ReflectCaller resolves the Device service through server reflection at
// run time, so no vendored .proto files can go stale against dish firmware.
// It mirrors what `grpcurl -plaintext 192.168.100.1:9200
// SpaceX.API.Device.Device/Handle` does.
type ReflectCaller struct {
	addr    string
	timeout time.Duration

	mu     sync.Mutex
	conn   *grpc.ClientConn
	refl   *grpcreflect.Client
	method *desc.MethodDescriptor
}

// NewReflectCaller returns a lazy caller; nothing is dialled until Call.
func NewReflectCaller(addr string, timeout time.Duration) *ReflectCaller {
	if addr == "" {
		addr = DefaultAddr
	}
	if timeout <= 0 {
		timeout = 4 * time.Second
	}
	return &ReflectCaller{addr: addr, timeout: timeout}
}

func (r *ReflectCaller) ensure(ctx context.Context) error {
	if r.method != nil {
		return nil
	}
	if r.conn == nil {
		conn, err := grpc.NewClient(r.addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return fmt.Errorf("%w: %v", ErrUnreachable, err)
		}
		r.conn = conn
	}
	// The reflection stream is long-lived; use a background context so it
	// is not torn down with the per-call context.
	r.refl = grpcreflect.NewClientAuto(context.Background(), r.conn)
	// New dish firmware omits some dependency files from reflection
	// responses (the "proto: not found" HA bug). Tolerate missing files
	// and fall back to the well-known types compiled into this binary.
	r.refl.AllowMissingFileDescriptors()
	r.refl.AllowFallbackResolver(protoregistry.GlobalFiles, protoregistry.GlobalTypes)

	done := make(chan error, 1)
	go func() {
		svc, err := r.refl.ResolveService(serviceName)
		if err != nil {
			done <- err
			return
		}
		m := svc.FindMethodByName(methodName)
		if m == nil {
			done <- fmt.Errorf("method %s not found on %s", methodName, serviceName)
			return
		}
		r.method = m
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			if isTransport(err) {
				return fmt.Errorf("%w: %v", ErrUnreachable, err)
			}
			return fmt.Errorf("%w: %v", ErrReflection, err)
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("%w: reflection timed out", ErrUnreachable)
	}
}

func isTransport(err error) bool {
	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled:
			return true
		}
	}
	s := err.Error()
	return strings.Contains(s, "connection refused") || strings.Contains(s, "no route") ||
		strings.Contains(s, "i/o timeout") || strings.Contains(s, "transport")
}

func (r *ReflectCaller) reset() {
	r.method = nil
	if r.refl != nil {
		r.refl.Reset()
		r.refl = nil
	}
	if r.conn != nil {
		_ = r.conn.Close()
		r.conn = nil
	}
}

// Call implements Caller.
func (r *ReflectCaller) Call(ctx context.Context, request map[string]any) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()
	if err := r.ensure(ctx); err != nil {
		r.reset()
		return nil, err
	}
	reqJSON, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	msg := dynamic.NewMessage(r.method.GetInputType())
	if err := msg.UnmarshalJSON(reqJSON); err != nil {
		return nil, fmt.Errorf("%w: request does not fit %s: %v", ErrReflection, r.method.GetInputType().GetFullyQualifiedName(), err)
	}
	resp, err := grpcdynamic.NewStub(r.conn).InvokeRpc(ctx, r.method, msg)
	if err != nil {
		if isTransport(err) {
			r.reset()
			return nil, fmt.Errorf("%w: %v", ErrUnreachable, err)
		}
		return nil, fmt.Errorf("starlink: Handle: %w", err)
	}
	dm, ok := resp.(*dynamic.Message)
	if !ok {
		// A generated type would only come back if the schema is linked in;
		// marshal it the same way.
		var sb strings.Builder
		if err := (&jsonpb.Marshaler{OrigName: true}).Marshal(&sb, resp); err != nil { //nolint:staticcheck
			return nil, err
		}
		return []byte(sb.String()), nil
	}
	return dm.MarshalJSONPB(&jsonpb.Marshaler{OrigName: true})
}

// Close releases the connection.
func (r *ReflectCaller) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reset()
	return nil
}

var _ Caller = (*ReflectCaller)(nil)
