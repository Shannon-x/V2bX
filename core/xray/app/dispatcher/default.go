package dispatcher

//go:generate go run github.com/xtls/xray-core/common/errors/errorgen

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/InazumaV/V2bX/common/counter"
	"github.com/InazumaV/V2bX/common/rate"
	"github.com/InazumaV/V2bX/limiter"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/errors"
	"github.com/xtls/xray-core/common/log"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/dns"
	"github.com/xtls/xray-core/features/outbound"
	"github.com/xtls/xray-core/features/policy"
	"github.com/xtls/xray-core/features/routing"
	routing_session "github.com/xtls/xray-core/features/routing/session"
	"github.com/xtls/xray-core/features/stats"
	"github.com/xtls/xray-core/transport"
	"github.com/xtls/xray-core/transport/pipe"
)

var errSniffingTimeout = errors.New("timeout on sniffing")

type cachedReader struct {
	sync.Mutex
	reader buf.TimeoutReader
	cache  buf.MultiBuffer
}

func (r *cachedReader) Cache(b *buf.Buffer, deadline time.Duration) error {
	mb, err := r.reader.ReadMultiBufferTimeout(deadline)
	if err != nil {
		return err
	}
	r.Lock()
	if !mb.IsEmpty() {
		r.cache, _ = buf.MergeMulti(r.cache, mb)
	}
	b.Clear()
	rawBytes := b.Extend(min(r.cache.Len(), b.Cap()))
	n := r.cache.Copy(rawBytes)
	b.Resize(0, int32(n))
	r.Unlock()
	return nil
}

func (r *cachedReader) readInternal() buf.MultiBuffer {
	r.Lock()
	defer r.Unlock()

	if r.cache != nil && !r.cache.IsEmpty() {
		mb := r.cache
		r.cache = nil
		return mb
	}

	return nil
}

func (r *cachedReader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	mb := r.readInternal()
	if mb != nil {
		return mb, nil
	}

	return r.reader.ReadMultiBuffer()
}

func (r *cachedReader) ReadMultiBufferTimeout(timeout time.Duration) (buf.MultiBuffer, error) {
	mb := r.readInternal()
	if mb != nil {
		return mb, nil
	}

	return r.reader.ReadMultiBufferTimeout(timeout)
}

func (r *cachedReader) Interrupt() {
	r.Lock()
	if r.cache != nil {
		r.cache = buf.ReleaseMulti(r.cache)
	}
	r.Unlock()
	if p, ok := r.reader.(*pipe.Reader); ok {
		p.Interrupt()
	}
}

// DefaultDispatcher is a default implementation of Dispatcher.
type DefaultDispatcher struct {
	ohm          outbound.Manager
	router       routing.Router
	policy       policy.Manager
	stats        stats.Manager
	fdns         dns.FakeDNSEngine
	Counter      sync.Map
	LinkManagers sync.Map // map[string]*LinkManager
}

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(ctx context.Context, config interface{}) (interface{}, error) {
		d := new(DefaultDispatcher)
		if err := core.RequireFeatures(ctx, func(om outbound.Manager, router routing.Router, pm policy.Manager, sm stats.Manager, dc dns.Client) error {
			core.OptionalFeatures(ctx, func(fdns dns.FakeDNSEngine) {
				d.fdns = fdns
			})
			return d.Init(config.(*Config), om, router, pm, sm)
		}); err != nil {
			return nil, err
		}
		return d, nil
	}))
}

// Init initializes DefaultDispatcher.
func (d *DefaultDispatcher) Init(config *Config, om outbound.Manager, router routing.Router, pm policy.Manager, sm stats.Manager) error {
	d.ohm = om
	d.router = router
	d.policy = pm
	d.stats = sm
	return nil
}

// Type implements common.HasType.
func (*DefaultDispatcher) Type() interface{} {
	return routing.DispatcherType()
}

// Start implements common.Runnable.
func (*DefaultDispatcher) Start() error {
	return nil
}

// Close implements common.Closable.
func (*DefaultDispatcher) Close() error { return nil }

func (d *DefaultDispatcher) getLink(ctx context.Context, network net.Network) (*transport.Link, *transport.Link, *limiter.Limiter, error) {
	opt := pipe.OptionsFromContext(ctx)
	uplinkReader, uplinkWriter := pipe.New(opt...)
	downlinkReader, downlinkWriter := pipe.New(opt...)

	inboundLink := &transport.Link{
		Reader: downlinkReader,
		Writer: uplinkWriter,
	}

	outboundLink := &transport.Link{
		Reader: uplinkReader,
		Writer: downlinkWriter,
	}

	sessionInbound := session.InboundFromContext(ctx)
	var user *protocol.MemoryUser
	if sessionInbound != nil {
		user = sessionInbound.User
	}

	var limit *limiter.Limiter
	var err error
	if user != nil && len(user.Email) > 0 {
		limit, err = limiter.GetLimiter(sessionInbound.Tag)
		if err != nil {
			errors.LogInfo(ctx, "get limiter ", sessionInbound.Tag, " error: ", err)
			common.Close(outboundLink.Writer)
			common.Close(inboundLink.Writer)
			common.Interrupt(outboundLink.Reader)
			common.Interrupt(inboundLink.Reader)
			return nil, nil, nil, errors.New("get limiter ", sessionInbound.Tag, " error: ", err)
		}
		// Speed Limit and Device Limit
		w, reject := limit.CheckLimit(user.Email,
			sessionInbound.Source.Address.IP().String(),
			network == net.Network_TCP,
			sessionInbound.Source.Network == net.Network_TCP)
		if reject {
			errors.LogInfo(ctx, "Limited ", user.Email, " by conn or ip")
			common.Close(outboundLink.Writer)
			common.Close(inboundLink.Writer)
			common.Interrupt(outboundLink.Reader)
			common.Interrupt(inboundLink.Reader)
			return nil, nil, nil, errors.New("Limited ", user.Email, " by conn or ip")
		}
		// W2.5 / W6 / audit #1 #29 #39 / B1: Load-first fast path; only
		// alloc a fresh LinkManager{links: make(map[...])} on cold miss.
		// Previous LoadOrStore always allocated and discarded on hit (each
		// hit threw one map + one struct at GC).
		var lm *LinkManager
		if v, ok := d.LinkManagers.Load(user.Email); ok {
			lm = v.(*LinkManager)
		} else {
			lmActual, _ := d.LinkManagers.LoadOrStore(user.Email, &LinkManager{
				links: make(map[*ManagedWriter]buf.Reader),
			})
			lm = lmActual.(*LinkManager)
		}
		managedWriter := newManagedWriter(uplinkWriter, lm)
		lm.AddLink(managedWriter, outboundLink.Reader)
		inboundLink.Writer = managedWriter
		if w != nil {
			sessionInbound.CanSpliceCopy = 3
			inboundLink.Writer = rate.NewRateLimitWriter(inboundLink.Writer, w)
			outboundLink.Writer = rate.NewRateLimitWriter(outboundLink.Writer, w)
		}
		// W2.5 / W6 / audit #28 / B1: Load-first; only alloc on cold miss.
		var t *counter.TrafficCounter
		if v, ok := d.Counter.Load(sessionInbound.Tag); ok {
			t = v.(*counter.TrafficCounter)
		} else {
			tActual, _ := d.Counter.LoadOrStore(sessionInbound.Tag, counter.NewTrafficCounter())
			t = tActual.(*counter.TrafficCounter)
		}

		ts := t.GetCounter(user.Email)
		upcounter := &counter.XrayTrafficCounter{V: &ts.UpCounter}
		downcounter := &counter.XrayTrafficCounter{V: &ts.DownCounter}
		// W6 / B3: wire the dirty-marker so this user shows up in the
		// next IterateDirty without GetUserTrafficSlice having to Range
		// the full Counters map.
		inboundLink.Writer = &SizeStatWriter{
			Counter: upcounter,
			Writer:  inboundLink.Writer,
			Parent:  t,
			UUID:    user.Email,
		}
		outboundLink.Writer = &SizeStatWriter{
			Counter: downcounter,
			Writer:  outboundLink.Writer,
			Parent:  t,
			UUID:    user.Email,
		}
	}

	return inboundLink, outboundLink, limit, nil
}

func (d *DefaultDispatcher) shouldOverride(ctx context.Context, result SniffResult, request session.SniffingRequest, destination net.Destination) bool {
	domain := result.Domain()
	if domain == "" {
		return false
	}
	if request.ExcludeForDomain != nil && request.ExcludeForDomain.MatchAny(strings.ToLower(domain)) {
		return false
	}
	protocolString := result.Protocol()
	if resComp, ok := result.(SnifferResultComposite); ok {
		protocolString = resComp.ProtocolForDomainResult()
	}
	for _, p := range request.OverrideDestinationForProtocol {
		if strings.HasPrefix(protocolString, p) || strings.HasPrefix(p, protocolString) {
			return true
		}
		if fkr0, ok := d.fdns.(dns.FakeDNSEngineRev0); ok && protocolString != "bittorrent" && p == "fakedns" &&
			destination.Address.Family().IsIP() && fkr0.IsIPInIPPool(destination.Address) {
			errors.LogInfo(ctx, "Using sniffer ", protocolString, " since the fake DNS missed")
			return true
		}
		if resultSubset, ok := result.(SnifferIsProtoSubsetOf); ok {
			if resultSubset.IsProtoSubsetOf(p) {
				return true
			}
		}
	}

	return false
}

// Dispatch implements routing.Dispatcher.
func (d *DefaultDispatcher) Dispatch(ctx context.Context, destination net.Destination) (*transport.Link, error) {
	if !destination.IsValid() {
		panic("Dispatcher: Invalid destination.")
	}
	outbounds := session.OutboundsFromContext(ctx)
	if len(outbounds) == 0 {
		outbounds = []*session.Outbound{{}}
		ctx = session.ContextWithOutbounds(ctx, outbounds)
	}
	ob := outbounds[len(outbounds)-1]
	ob.OriginalTarget = destination
	ob.Target = destination
	content := session.ContentFromContext(ctx)
	if content == nil {
		content = new(session.Content)
		ctx = session.ContextWithContent(ctx, content)
	}
	sniffingRequest := content.SniffingRequest
	inbound, outbound, l, err := d.getLink(ctx, destination.Network)
	if err != nil {
		return nil, err
	}
	if !sniffingRequest.Enabled {
		go d.routedDispatch(ctx, outbound, destination, l, "")
	} else {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					errors.LogError(ctx, "panic in dispatcher sniffing: ", fmt.Sprint(r))
					common.Close(outbound.Writer)
					common.Interrupt(outbound.Reader)
				}
			}()
			cReader := &cachedReader{
				reader: outbound.Reader.(*pipe.Reader),
			}
			outbound.Reader = cReader
			result, err := sniffer(ctx, cReader, sniffingRequest.MetadataOnly, destination.Network)
			if err == nil {
				content.Protocol = result.Protocol()
			}
			if err == nil && d.shouldOverride(ctx, result, sniffingRequest, destination) {
				domain := result.Domain()
				errors.LogInfo(ctx, "sniffed domain: ", domain)
				destination.Address = net.ParseAddress(domain)
				protocol := result.Protocol()
				if resComp, ok := result.(SnifferResultComposite); ok {
					protocol = resComp.ProtocolForDomainResult()
				}
				isFakeIP := false
				if fkr0, ok := d.fdns.(dns.FakeDNSEngineRev0); ok && ob.Target.Address.Family().IsIP() && fkr0.IsIPInIPPool(ob.Target.Address) {
					isFakeIP = true
				}
				if sniffingRequest.RouteOnly && protocol != "fakedns" && protocol != "fakedns+others" && !isFakeIP {
					ob.RouteTarget = destination
				} else {
					ob.Target = destination
				}
			}
			d.routedDispatch(ctx, outbound, destination, l, content.Protocol)
		}()
	}
	return inbound, nil
}

// DispatchLink implements routing.Dispatcher.
func (d *DefaultDispatcher) DispatchLink(ctx context.Context, destination net.Destination, outbound *transport.Link) error {
	if !destination.IsValid() {
		return errors.New("Dispatcher: Invalid destination.")
	}
	outbounds := session.OutboundsFromContext(ctx)
	if len(outbounds) == 0 {
		outbounds = []*session.Outbound{{}}
		ctx = session.ContextWithOutbounds(ctx, outbounds)
	}
	ob := outbounds[len(outbounds)-1]
	ob.OriginalTarget = destination
	ob.Target = destination
	content := session.ContentFromContext(ctx)
	if content == nil {
		content = new(session.Content)
		ctx = session.ContextWithContent(ctx, content)
	}

	sessionInbound := session.InboundFromContext(ctx)
	var user *protocol.MemoryUser
	if sessionInbound != nil {
		user = sessionInbound.User
	}

	var limit *limiter.Limiter
	var err error
	if user != nil && len(user.Email) > 0 {
		limit, err = limiter.GetLimiter(sessionInbound.Tag)
		if err != nil {
			errors.LogInfo(ctx, "get limiter ", sessionInbound.Tag, " error: ", err)
			common.Close(outbound.Writer)
			common.Interrupt(outbound.Reader)
			return errors.New("get limiter ", sessionInbound.Tag, " error: ", err)
		}
		// Speed Limit and Device Limit
		w, reject := limit.CheckLimit(user.Email,
			sessionInbound.Source.Address.IP().String(),
			destination.Network == net.Network_TCP,
			sessionInbound.Source.Network == net.Network_TCP)
		if reject {
			errors.LogInfo(ctx, "Limited ", user.Email, " by conn or ip")
			common.Close(outbound.Writer)
			common.Interrupt(outbound.Reader)
			return errors.New("Limited ", user.Email, " by conn or ip")
		}
		// W2.5 / audit #1 #29 #39: same LoadOrStore fix as in getLink.
		lmActual, _ := d.LinkManagers.LoadOrStore(user.Email, &LinkManager{
			links: make(map[*ManagedWriter]buf.Reader),
		})
		lm := lmActual.(*LinkManager)
		managedWriter := newManagedWriter(outbound.Writer, lm)
		outbound.Writer = managedWriter
		if w != nil {
			sessionInbound.CanSpliceCopy = 3
			outbound.Writer = rate.NewRateLimitWriter(outbound.Writer, w)
		}
		// W2.5 / W6 / audit #28 / B1: Load-first; only alloc on cold miss.
		var t *counter.TrafficCounter
		if v, ok := d.Counter.Load(sessionInbound.Tag); ok {
			t = v.(*counter.TrafficCounter)
		} else {
			tActual, _ := d.Counter.LoadOrStore(sessionInbound.Tag, counter.NewTrafficCounter())
			t = tActual.(*counter.TrafficCounter)
		}

		ts := t.GetCounter(user.Email)
		downcounter := &counter.XrayTrafficCounter{V: &ts.DownCounter}
		// W6 / B3: dirty-marker wiring (see getLink for rationale).
		outbound.Reader = &CounterReader{
			Reader:  &buf.TimeoutWrapperReader{Reader: outbound.Reader},
			Counter: &ts.UpCounter,
			Parent:  t,
			UUID:    user.Email,
		}
		lm.AddLink(managedWriter, outbound.Reader)
		outbound.Writer = &SizeStatWriter{
			Counter: downcounter,
			Writer:  outbound.Writer,
			Parent:  t,
			UUID:    user.Email,
		}
	}

	sniffingRequest := content.SniffingRequest
	if !sniffingRequest.Enabled {
		d.routedDispatch(ctx, outbound, destination, limit, "")
	} else {
		cReader := &cachedReader{
			reader: outbound.Reader.(buf.TimeoutReader),
		}
		outbound.Reader = cReader
		result, err := sniffer(ctx, cReader, sniffingRequest.MetadataOnly, destination.Network)
		if err == nil {
			content.Protocol = result.Protocol()
		}
		if err == nil && d.shouldOverride(ctx, result, sniffingRequest, destination) {
			domain := result.Domain()
			errors.LogInfo(ctx, "sniffed domain: ", domain)
			destination.Address = net.ParseAddress(domain)
			protocol := result.Protocol()
			if resComp, ok := result.(SnifferResultComposite); ok {
				protocol = resComp.ProtocolForDomainResult()
			}
			isFakeIP := false
			if fkr0, ok := d.fdns.(dns.FakeDNSEngineRev0); ok && fkr0.IsIPInIPPool(ob.Target.Address) {
				isFakeIP = true
			}
			if sniffingRequest.RouteOnly && protocol != "fakedns" && protocol != "fakedns+others" && !isFakeIP {
				ob.RouteTarget = destination
			} else {
				ob.Target = destination
			}
		}
		d.routedDispatch(ctx, outbound, destination, limit, content.Protocol)
	}

	return nil
}

func sniffer(ctx context.Context, cReader *cachedReader, metadataOnly bool, network net.Network) (SniffResult, error) {
	payload := buf.NewWithSize(32767)
	defer payload.Release()

	sniffer := NewSniffer(ctx)

	metaresult, metadataErr := sniffer.SniffMetadata(ctx)

	if metadataOnly {
		return metaresult, metadataErr
	}

	contentResult, contentErr := func() (SniffResult, error) {
		cacheDeadline := 200 * time.Millisecond
		totalAttempt := 0
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			default:
				cachingStartingTimeStamp := time.Now()
				err := cReader.Cache(payload, cacheDeadline)
				if err != nil {
					return nil, err
				}
				cachingTimeElapsed := time.Since(cachingStartingTimeStamp)
				cacheDeadline -= cachingTimeElapsed

				if !payload.IsEmpty() {
					result, err := sniffer.Sniff(ctx, payload.Bytes(), network)
					switch err {
					case common.ErrNoClue: // No Clue: protocol not matches, and sniffer cannot determine whether there will be a match or not
						totalAttempt++
					case protocol.ErrProtoNeedMoreData: // Protocol Need More Data: protocol matches, but need more data to complete sniffing
						// in this case, do not add totalAttempt(allow to read until timeout)
					default:
						return result, err
					}
				} else {
					totalAttempt++
				}
				if totalAttempt >= 2 || cacheDeadline <= 0 {
					return nil, errSniffingTimeout
				}
			}
		}
	}()
	if contentErr != nil && metadataErr == nil {
		return metaresult, nil
	}
	if contentErr == nil && metadataErr == nil {
		return CompositeResult(metaresult, contentResult), nil
	}
	return contentResult, contentErr
}

func (d *DefaultDispatcher) routedDispatch(ctx context.Context, link *transport.Link, destination net.Destination, l *limiter.Limiter, protocol string) {
	outbounds := session.OutboundsFromContext(ctx)
	ob := outbounds[len(outbounds)-1]

	// W1.10 / audit #40: InboundFromContext can return nil if no inbound
	// metadata was attached. Dereferencing sessionInbound.User would panic in
	// a goroutine without recover and kill the process.
	sessionInbound := session.InboundFromContext(ctx)
	if sessionInbound != nil && sessionInbound.User != nil {
		if l == nil {
			var err error
			l, err = limiter.GetLimiter(sessionInbound.Tag)
			if err != nil {
				errors.LogError(ctx, "get limiter ", sessionInbound.Tag, " error: ", err)
			}
		}
		if l != nil {
			var destStr string
			var destIP string
			if destination.Address.Family().IsDomain() {
				destStr = destination.Address.Domain()
			} else {
				destIP = destination.Address.IP().String()
				destStr = destIP
			}
			// Block domain rules
			if l.CheckDomainRule(destStr) {
				errors.LogError(ctx, fmt.Sprintf(
					"User %s access domain %s reject by rule",
					sessionInbound.User.Email,
					destStr))
				common.Close(link.Writer)
				common.Interrupt(link.Reader)
				return
			}
			// Block IP rules
			if destIP != "" && l.CheckIPRule(destIP) {
				errors.LogError(ctx, fmt.Sprintf(
					"User %s access IP %s reject by rule",
					sessionInbound.User.Email,
					destIP))
				common.Close(link.Writer)
				common.Interrupt(link.Reader)
				return
			}
			// Block port rules
			if l.CheckPortRule(int(destination.Port)) {
				errors.LogError(ctx, fmt.Sprintf(
					"User %s access port %d reject by rule",
					sessionInbound.User.Email,
					destination.Port))
				common.Close(link.Writer)
				common.Interrupt(link.Reader)
				return
			}
			// Protocol rules
			if len(protocol) != 0 {
				if l.CheckProtocolRule(protocol) {
					errors.LogError(ctx, fmt.Sprintf(
						"User %s access protocol %s reject by rule",
						sessionInbound.User.Email,
						protocol))
					common.Close(link.Writer)
					common.Interrupt(link.Reader)
					return
				}
			}
			// Route rules (route/route_ip/direct/proxy)
			if routeTag := l.CheckRouteRule(destStr, destIP); routeTag != "" {
				errors.LogInfo(ctx, fmt.Sprintf(
					"User %s route %s to outbound [%s] by rule",
					sessionInbound.User.Email,
					destStr,
					routeTag))
				if h := d.ohm.GetHandler(routeTag); h != nil {
					ob.Tag = h.Tag()
					if accessMessage := log.AccessMessageFromContext(ctx); accessMessage != nil {
						accessMessage.Detour = sessionInbound.Tag + " => " + h.Tag()
						log.Record(accessMessage)
					}
					h.Dispatch(ctx, link)
					return
				}
			}
		}
	}

	var handler outbound.Handler

	routingLink := routing_session.AsRoutingContext(ctx)
	inTag := routingLink.GetInboundTag()
	isPickRoute := 0
	if forcedOutboundTag := session.GetForcedOutboundTagFromContext(ctx); forcedOutboundTag != "" {
		ctx = session.SetForcedOutboundTagToContext(ctx, "")
		if h := d.ohm.GetHandler(forcedOutboundTag); h != nil {
			isPickRoute = 1
			errors.LogInfo(ctx, "taking platform initialized detour [", forcedOutboundTag, "] for [", destination, "]")
			handler = h
		} else {
			errors.LogError(ctx, "non existing tag for platform initialized detour: ", forcedOutboundTag)
			common.Close(link.Writer)
			common.Interrupt(link.Reader)
			return
		}
	} else if d.router != nil {
		if route, err := d.router.PickRoute(routingLink); err == nil {
			outTag := route.GetOutboundTag()
			if h := d.ohm.GetHandler(outTag); h != nil {
				isPickRoute = 2
				if route.GetRuleTag() == "" {
					errors.LogInfo(ctx, "taking detour [", outTag, "] for [", destination, "]")
				} else {
					errors.LogInfo(ctx, "Hit route rule: [", route.GetRuleTag(), "] so taking detour [", outTag, "] for [", destination, "]")
				}
				handler = h
			} else {
				errors.LogWarning(ctx, "non existing outTag: ", outTag)
				common.Close(link.Writer)
				common.Interrupt(link.Reader)
				return // DO NOT CHANGE: the traffic shouldn't be processed by default outbound if the specified outbound tag doesn't exist (yet), e.g., VLESS Reverse Proxy
			}
		} else {
			errors.LogInfo(ctx, "default route for ", destination)
		}
	}

	if handler == nil {
		if l != nil && l.DefaultOutbound != "" {
			if h := d.ohm.GetHandler(l.DefaultOutbound); h != nil {
				errors.LogInfo(ctx, "taking custom default_out detour [", l.DefaultOutbound, "] for [", destination, "]")
				handler = h
			} else {
				errors.LogWarning(ctx, "custom default_out tag non existing: ", l.DefaultOutbound)
			}
		}
	}

	if handler == nil {
		handler = d.ohm.GetDefaultHandler()
	}

	if handler == nil {
		errors.LogInfo(ctx, "default outbound handler not exist")
		common.Close(link.Writer)
		common.Interrupt(link.Reader)
		return
	}

	ob.Tag = handler.Tag()
	if accessMessage := log.AccessMessageFromContext(ctx); accessMessage != nil {
		if tag := handler.Tag(); tag != "" {
			if inTag == "" {
				accessMessage.Detour = tag
			} else if isPickRoute == 1 {
				accessMessage.Detour = inTag + " ==> " + tag
			} else if isPickRoute == 2 {
				accessMessage.Detour = inTag + " -> " + tag
			} else {
				accessMessage.Detour = inTag + " >> " + tag
			}
		}
		log.Record(accessMessage)
	}

	handler.Dispatch(ctx, link)
}
