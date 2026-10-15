package media

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// Options configure HTTPResolver.
type Options struct {
	// MaxBytes is the largest accepted image, decoded.
	MaxBytes int64
	// FetchURLs allows http and https URLs; otherwise only data: URLs.
	FetchURLs bool
	// AllowPrivateNetworks allows fetching from loopback, private,
	// link-local and other non-public addresses.
	AllowPrivateNetworks bool
	// Timeout bounds each download.
	Timeout time.Duration
}

// HTTPResolver resolves data: URLs and, if enabled, downloads http(s) URLs.
type HTTPResolver struct {
	opts   Options
	client *http.Client
}

var _ Resolver = (*HTTPResolver)(nil)

// NewHTTPResolver returns a resolver. Downloads refuse non-public addresses
// unless opts.AllowPrivateNetworks is set; the check runs on the resolved
// address of every connection, redirects included.
func NewHTTPResolver(opts Options) *HTTPResolver {
	if opts.Timeout <= 0 {
		opts.Timeout = 20 * time.Second
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if !opts.AllowPrivateNetworks {
		dialer.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || !publicIP(ip) {
				return fmt.Errorf("%w: address %s is not public", ErrInvalid, host)
			}
			return nil
		}
	}
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: opts.Timeout,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
	}
	if !opts.AllowPrivateNetworks {
		// A proxy would hide the real destination from the address check.
		transport.Proxy = nil
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   opts.Timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
				return fmt.Errorf("redirect to unsupported scheme %q", req.URL.Scheme)
			}
			return nil
		},
	}
	return &HTTPResolver{opts: opts, client: client}
}

func publicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		ip.Equal(net.IPv4bcast) || isCGNAT(ip))
}

// isCGNAT reports addresses in 100.64.0.0/10 (shared address space).
func isCGNAT(ip net.IP) bool {
	v4 := ip.To4()
	return v4 != nil && v4[0] == 100 && v4[1]&0xC0 == 64
}

// Resolve implements Resolver.
func (r *HTTPResolver) Resolve(ctx context.Context, url string) (Image, error) {
	var data []byte
	var err error
	switch {
	case strings.HasPrefix(url, "data:"):
		data, err = r.decodeDataURL(url)
	case strings.HasPrefix(url, "https://"), strings.HasPrefix(url, "http://"):
		if !r.opts.FetchURLs {
			return Image{}, fmt.Errorf("%w: image URLs are disabled, send the image as a data: URL", ErrInvalid)
		}
		data, err = r.download(ctx, url)
	default:
		return Image{}, fmt.Errorf("%w: unsupported image URL scheme", ErrInvalid)
	}
	if err != nil {
		return Image{}, err
	}
	mt := http.DetectContentType(data)
	if !supportedTypes[mt] {
		return Image{}, fmt.Errorf("%w: unsupported image type %q (png, jpeg, gif and webp are supported)", ErrInvalid, mt)
	}
	return Image{MediaType: mt, Data: data}, nil
}

func (r *HTTPResolver) tooLarge() error {
	return fmt.Errorf("%w: image larger than %d bytes", ErrInvalid, r.opts.MaxBytes)
}

func (r *HTTPResolver) decodeDataURL(url string) ([]byte, error) {
	meta, payload, ok := strings.Cut(strings.TrimPrefix(url, "data:"), ",")
	if !ok || !strings.HasSuffix(meta, ";base64") {
		return nil, fmt.Errorf("%w: data URL must be base64 encoded", ErrInvalid)
	}
	if r.opts.MaxBytes > 0 && int64(base64.StdEncoding.DecodedLen(len(payload))) > r.opts.MaxBytes+3 {
		return nil, r.tooLarge()
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		if data, err = base64.RawStdEncoding.DecodeString(payload); err != nil {
			return nil, fmt.Errorf("%w: bad base64 in data URL", ErrInvalid)
		}
	}
	if r.opts.MaxBytes > 0 && int64(len(data)) > r.opts.MaxBytes {
		return nil, r.tooLarge()
	}
	return data, nil
}

func (r *HTTPResolver) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	req.Header.Set("Accept", "image/png, image/jpeg, image/gif, image/webp")
	resp, err := r.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrInvalid) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: download failed: %v", ErrInvalid, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: download returned HTTP %d", ErrInvalid, resp.StatusCode)
	}
	limit := r.opts.MaxBytes
	if limit <= 0 {
		limit = 1 << 62
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: download failed: %v", ErrInvalid, err)
	}
	if int64(len(data)) > limit {
		return nil, r.tooLarge()
	}
	return data, nil
}
