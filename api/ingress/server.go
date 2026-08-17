package ingress

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"farmail/store"

	"github.com/google/uuid"
)

type Config struct {
	Addr              string
	Hostname          string
	BodyMaxBytes      int
	StoreBodyMaxBytes int
	Workers           int
	QueueSize         int
	DeliveryTimeout   time.Duration
	CacheRefresh      time.Duration
	SessionTimeout    time.Duration
	CommandMaxBytes   int
}

func ConfigFromEnv(hostname string) Config {
	return Config{
		Addr:              envString("LMTP_ADDR", "0.0.0.0:2527"),
		Hostname:          firstNonEmpty(hostname, envString("SMTP_HOSTNAME", "far-mail.local")),
		BodyMaxBytes:      envInt("INGRESS_BODY_MAX_BYTES", 262144),
		StoreBodyMaxBytes: envInt("INGRESS_STORE_BODY_MAX_BYTES", 65536),
		Workers:           envInt("INGRESS_WORKERS", 48),
		QueueSize:         envInt("INGRESS_QUEUE_SIZE", 2048),
		DeliveryTimeout:   time.Duration(envInt("INGRESS_DELIVERY_TIMEOUT_SECONDS", 8)) * time.Second,
		CacheRefresh:      time.Duration(envInt("INGRESS_CACHE_REFRESH_SECONDS", 30)) * time.Second,
		SessionTimeout:    300 * time.Second,
		CommandMaxBytes:   8192,
	}
}

type Server struct {
	cfg    Config
	store  *store.Store
	jobs   chan deliveryJob
	slots  chan struct{}
	snap   atomic.Value // *snapshot
	ln     net.Listener
	cancel context.CancelFunc
	wg     sync.WaitGroup

	eventMu             sync.Mutex
	eventSubscribers    map[uint64]emailSubscription
	nextEventSubscriber uint64

	connectionsAccepted atomic.Uint64
	activeConnections   atomic.Int64
	jobsSubmitted       atomic.Uint64
	jobsDelivered       atomic.Uint64
	jobsTempFailed      atomic.Uint64
	jobsPermFailed      atomic.Uint64
	queueFull           atomic.Uint64
	queueHighWater      atomic.Int64
	inFlightHighWater   atomic.Int64
	oversizedMessages   atomic.Uint64
	deliveryTimeouts    atomic.Uint64
	jobsCancelled       atomic.Uint64
	dataBytes           atomic.Uint64
	activeWorkers       atomic.Int64
	parseOps            atomic.Uint64
	parseNanos          atomic.Uint64
	dbOps               atomic.Uint64
	dbNanos             atomic.Uint64
	eventsDropped       atomic.Uint64
}

type Stats struct {
	Addr                string  `json:"addr"`
	Workers             int     `json:"workers"`
	QueueSize           int     `json:"queue_size"`
	QueueDepth          int     `json:"queue_depth"`
	QueueHighWater      int64   `json:"queue_high_water"`
	InFlight            int     `json:"in_flight"`
	InFlightHighWater   int64   `json:"in_flight_high_water"`
	ActiveWorkers       int64   `json:"active_workers"`
	ActiveConnections   int64   `json:"active_connections"`
	ConnectionsAccepted uint64  `json:"connections_accepted"`
	JobsSubmitted       uint64  `json:"jobs_submitted"`
	JobsDelivered       uint64  `json:"jobs_delivered"`
	JobsTempFailed      uint64  `json:"jobs_temp_failed"`
	JobsPermFailed      uint64  `json:"jobs_perm_failed"`
	QueueFull           uint64  `json:"queue_full"`
	OversizedMessages   uint64  `json:"oversized_messages"`
	DeliveryTimeouts    uint64  `json:"delivery_timeouts"`
	JobsCancelled       uint64  `json:"jobs_cancelled"`
	DataBytes           uint64  `json:"data_bytes"`
	AvgParseMs          float64 `json:"avg_parse_ms"`
	AvgDBMs             float64 `json:"avg_db_ms"`
	BodyMaxBytes        int     `json:"body_max_bytes"`
	StoreBodyMaxBytes   int     `json:"store_body_max_bytes"`
	SnapshotDomains     int     `json:"snapshot_domains"`
	SnapshotLoadedAt    string  `json:"snapshot_loaded_at,omitempty"`
	EventSubscribers    int     `json:"event_subscribers"`
	EventsDropped       uint64  `json:"events_dropped"`
}

type snapshot struct {
	domains           []snapshotDomain
	domainsByName     map[string]snapshotDomain
	defaultAccountID  uuid.UUID
	mailboxTTLMinutes int
	loadedAt          time.Time
	fingerprint       string
}

type snapshotDomain struct {
	id     int
	domain string
}

type resolvedRecipient struct {
	address           string
	localPart         string
	domainName        string
	domainID          int
	accountID         uuid.UUID
	mailboxTTLMinutes int
}

type deliveryJob struct {
	ctx            context.Context
	result         chan deliveryResult
	resolved       resolvedRecipient
	envelopeSender string
	raw            []byte
}

type deliveryResult struct {
	status deliveryStatus
	err    error
}

type deliveryStatus int

const (
	deliveryOK deliveryStatus = iota
	deliveryTempFail
	deliveryPermFail
)

func NewServer(db *store.Store, cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = "0.0.0.0:2527"
	}
	if cfg.Hostname == "" {
		cfg.Hostname = "far-mail.local"
	}
	if cfg.BodyMaxBytes <= 0 {
		cfg.BodyMaxBytes = 262144
	}
	if cfg.StoreBodyMaxBytes <= 0 {
		cfg.StoreBodyMaxBytes = 65536
	}
	if cfg.StoreBodyMaxBytes > cfg.BodyMaxBytes {
		cfg.StoreBodyMaxBytes = cfg.BodyMaxBytes
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 48
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = 2048
	}
	if cfg.DeliveryTimeout <= 0 {
		cfg.DeliveryTimeout = 8 * time.Second
	}
	if cfg.CacheRefresh <= 0 {
		cfg.CacheRefresh = 30 * time.Second
	}
	if cfg.SessionTimeout <= 0 {
		cfg.SessionTimeout = 300 * time.Second
	}
	if cfg.CommandMaxBytes <= 0 {
		cfg.CommandMaxBytes = 8192
	}

	s := &Server{
		cfg:   cfg,
		store: db,
		jobs:  make(chan deliveryJob, cfg.QueueSize),
		slots: make(chan struct{}, cfg.QueueSize),
	}
	for i := 0; i < cfg.QueueSize; i++ {
		s.slots <- struct{}{}
	}
	return s
}

func (s *Server) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	s.cancel = cancel

	if err := s.refreshSnapshot(ctx); err != nil {
		log.Printf("[ingress] initial snapshot refresh failed: %v", err)
	}

	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		cancel()
		return err
	}
	s.ln = ln

	for i := 0; i < s.cfg.Workers; i++ {
		s.wg.Add(1)
		go s.worker(ctx, i)
	}

	s.wg.Add(1)
	go s.refreshLoop(ctx)

	s.wg.Add(1)
	go s.acceptLoop(ctx)
	log.Printf("✓ Go LMTP ingress listening on %s (workers=%d, queue=%d, body_max=%d, store_body_max=%d)", s.cfg.Addr, s.cfg.Workers, s.cfg.QueueSize, s.cfg.BodyMaxBytes, s.cfg.StoreBodyMaxBytes)
	return nil
}

func (s *Server) Stats() Stats {
	parseOps := s.parseOps.Load()
	dbOps := s.dbOps.Load()
	var avgParseMs, avgDBMs float64
	if parseOps > 0 {
		avgParseMs = float64(s.parseNanos.Load()) / float64(parseOps) / float64(time.Millisecond)
	}
	if dbOps > 0 {
		avgDBMs = float64(s.dbNanos.Load()) / float64(dbOps) / float64(time.Millisecond)
	}
	inFlight := s.cfg.QueueSize - len(s.slots)
	if inFlight < 0 {
		inFlight = 0
	}
	stats := Stats{
		Addr:                s.cfg.Addr,
		Workers:             s.cfg.Workers,
		QueueSize:           s.cfg.QueueSize,
		QueueDepth:          len(s.jobs),
		QueueHighWater:      s.queueHighWater.Load(),
		InFlight:            inFlight,
		InFlightHighWater:   s.inFlightHighWater.Load(),
		ActiveWorkers:       s.activeWorkers.Load(),
		ActiveConnections:   s.activeConnections.Load(),
		ConnectionsAccepted: s.connectionsAccepted.Load(),
		JobsSubmitted:       s.jobsSubmitted.Load(),
		JobsDelivered:       s.jobsDelivered.Load(),
		JobsTempFailed:      s.jobsTempFailed.Load(),
		JobsPermFailed:      s.jobsPermFailed.Load(),
		QueueFull:           s.queueFull.Load(),
		OversizedMessages:   s.oversizedMessages.Load(),
		DeliveryTimeouts:    s.deliveryTimeouts.Load(),
		JobsCancelled:       s.jobsCancelled.Load(),
		DataBytes:           s.dataBytes.Load(),
		AvgParseMs:          avgParseMs,
		AvgDBMs:             avgDBMs,
		BodyMaxBytes:        s.cfg.BodyMaxBytes,
		StoreBodyMaxBytes:   s.cfg.StoreBodyMaxBytes,
		EventSubscribers:    s.eventSubscriberCount(),
		EventsDropped:       s.eventsDropped.Load(),
	}
	if snap := s.currentSnapshot(); snap != nil {
		stats.SnapshotDomains = len(snap.domains)
		stats.SnapshotLoadedAt = snap.loadedAt.Format(time.RFC3339)
	}
	return stats
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.cancel != nil {
		s.cancel()
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		s.drainQueuedJobs(context.Canceled)
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) refreshLoop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(s.cfg.CacheRefresh)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.refreshSnapshot(ctx); err != nil {
				log.Printf("[ingress] snapshot refresh failed: %v", err)
			}
		}
	}
}

func (s *Server) refreshSnapshot(ctx context.Context) error {
	loaded, err := s.store.LoadIngressSnapshot(ctx)
	if err != nil {
		return err
	}
	snap := &snapshot{
		domains:           make([]snapshotDomain, 0, len(loaded.Domains)),
		domainsByName:     make(map[string]snapshotDomain, len(loaded.Domains)),
		defaultAccountID:  loaded.DefaultAccountID,
		mailboxTTLMinutes: loaded.MailboxTTLMinutes,
		loadedAt:          time.Now(),
	}
	for _, d := range loaded.Domains {
		domain := strings.ToLower(strings.Trim(strings.TrimSpace(d.Domain), "."))
		if d.ID <= 0 || domain == "" {
			continue
		}
		item := snapshotDomain{id: d.ID, domain: domain}
		snap.domains = append(snap.domains, item)
		snap.domainsByName[domain] = item
	}
	sort.Slice(snap.domains, func(i, j int) bool {
		if len(snap.domains[i].domain) == len(snap.domains[j].domain) {
			return snap.domains[i].domain < snap.domains[j].domain
		}
		return len(snap.domains[i].domain) > len(snap.domains[j].domain)
	})
	var fingerprint strings.Builder
	fingerprint.WriteString(snap.defaultAccountID.String())
	fingerprint.WriteByte('|')
	fingerprint.WriteString(strconv.Itoa(snap.mailboxTTLMinutes))
	for _, domain := range snap.domains {
		fingerprint.WriteByte('|')
		fingerprint.WriteString(strconv.Itoa(domain.id))
		fingerprint.WriteByte(':')
		fingerprint.WriteString(domain.domain)
	}
	snap.fingerprint = fingerprint.String()
	previous := s.currentSnapshot()
	s.snap.Store(snap)
	if previous == nil || previous.fingerprint != snap.fingerprint {
		log.Printf("[ingress] snapshot loaded: domains=%d ttl=%d", len(snap.domains), snap.mailboxTTLMinutes)
	}
	return nil
}

func (s *Server) currentSnapshot() *snapshot {
	v := s.snap.Load()
	if v == nil {
		return nil
	}
	snap, _ := v.(*snapshot)
	return snap
}

func (s *Server) acceptLoop(ctx context.Context) {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("[ingress] accept error: %v", err)
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			continue
		}
		s.connectionsAccepted.Add(1)
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleConn(ctx, conn)
		}()
	}
}

func (s *Server) worker(ctx context.Context, id int) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.jobs:
			s.handleJob(job)
		}
	}
}

func (s *Server) handleJob(job deliveryJob) {
	defer func() { s.slots <- struct{}{} }()
	s.activeWorkers.Add(1)
	defer s.activeWorkers.Add(-1)
	if err := job.ctx.Err(); err != nil {
		s.jobsTempFailed.Add(1)
		s.sendJobResult(job, deliveryResult{status: deliveryTempFail, err: err})
		return
	}
	parseStart := time.Now()
	parsed := parseMessage(job.raw, job.envelopeSender, s.cfg.StoreBodyMaxBytes)
	s.parseOps.Add(1)
	s.parseNanos.Add(uint64(time.Since(parseStart)))
	dbStart := time.Now()
	event, delivered, err := s.store.InsertEmailResolved(job.ctx, store.IngressDelivery{
		Recipient:         job.resolved.address,
		LocalPart:         job.resolved.localPart,
		DomainName:        job.resolved.domainName,
		AccountID:         job.resolved.accountID,
		DomainID:          job.resolved.domainID,
		MailboxTTLMinutes: job.resolved.mailboxTTLMinutes,
		Sender:            parsed.Sender,
		Subject:           parsed.Subject,
		BodyText:          parsed.BodyText,
		BodyHTML:          parsed.BodyHTML,
		HasAttachments:    parsed.HasAttachments,
		Raw:               "",
		SizeBytes:         len(job.raw),
	})
	s.dbOps.Add(1)
	s.dbNanos.Add(uint64(time.Since(dbStart)))
	if err != nil {
		s.jobsTempFailed.Add(1)
		s.sendJobResult(job, deliveryResult{status: deliveryTempFail, err: err})
		return
	}
	if !delivered {
		s.jobsPermFailed.Add(1)
		s.sendJobResult(job, deliveryResult{status: deliveryPermFail, err: fmt.Errorf("recipient not deliverable")})
		return
	}
	s.jobsDelivered.Add(1)
	if event != nil {
		s.publishEmailEvent(*event)
	}
	s.sendJobResult(job, deliveryResult{status: deliveryOK})
}

func (s *Server) sendJobResult(job deliveryJob, result deliveryResult) {
	select {
	case job.result <- result:
	default:
	}
}

func (s *Server) acquireSlot() bool {
	select {
	case <-s.slots:
		recordHighWater(&s.inFlightHighWater, int64(s.cfg.QueueSize-len(s.slots)))
		return true
	default:
		return false
	}
}

func (s *Server) submitJob(ctx context.Context, job deliveryJob) bool {
	select {
	case s.jobs <- job:
		recordHighWater(&s.queueHighWater, int64(len(s.jobs)))
		return true
	case <-ctx.Done():
		return false
	}
}

func (s *Server) drainQueuedJobs(cause error) {
	for {
		select {
		case job := <-s.jobs:
			s.jobsCancelled.Add(1)
			s.jobsTempFailed.Add(1)
			s.sendJobResult(job, deliveryResult{status: deliveryTempFail, err: cause})
			s.slots <- struct{}{}
		default:
			return
		}
	}
}

func recordHighWater(target *atomic.Int64, value int64) {
	for previous := target.Load(); value > previous; previous = target.Load() {
		if target.CompareAndSwap(previous, value) {
			return
		}
	}
}

func (s *Server) handleConn(parent context.Context, conn net.Conn) {
	defer conn.Close()
	s.activeConnections.Add(1)
	defer s.activeConnections.Add(-1)
	remote := conn.RemoteAddr().String()
	reader := bufio.NewReaderSize(conn, 64*1024)
	writer := bufio.NewWriterSize(conn, 16*1024)
	session := &lmtpSession{server: s, conn: conn, reader: reader, writer: writer, parent: parent, remote: remote}
	session.run()
}

type lmtpSession struct {
	server *Server
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
	parent context.Context
	remote string
	helo   string
	txn    transaction
}

type transaction struct {
	hasMail   bool
	mailFrom  string
	recipient *resolvedRecipient
}

func (s *lmtpSession) run() {
	if !s.writeLine("220 %s LMTP ready", s.server.cfg.Hostname) {
		return
	}
	for {
		line, err := s.readCommandLine()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.Printf("[ingress] %s read command error: %v", s.remote, err)
			}
			return
		}
		if line == "" {
			_ = s.writeLine("500 5.5.2 empty command")
			continue
		}
		cmd, arg := splitCommand(line)
		switch cmd {
		case "LHLO", "EHLO", "HELO":
			s.helo = strings.TrimSpace(arg)
			s.txn = transaction{}
			if !s.writeCapabilities() {
				return
			}
		case "MAIL":
			s.handleMAIL(arg)
		case "RCPT":
			s.handleRCPT(arg)
		case "DATA":
			if !s.handleDATA() {
				return
			}
		case "RSET":
			s.txn = transaction{}
			_ = s.writeLine("250 2.0.0 OK")
		case "NOOP":
			_ = s.writeLine("250 2.0.0 OK")
		case "QUIT":
			_ = s.writeLine("221 2.0.0 Bye")
			return
		default:
			_ = s.writeLine("500 5.5.1 command unrecognized")
		}
	}
}

func (s *lmtpSession) handleMAIL(arg string) {
	path, ok := extractPathArg(arg, "FROM:", true)
	if !ok {
		_ = s.writeLine("501 5.5.4 bad sender syntax")
		return
	}
	s.txn = transaction{hasMail: true, mailFrom: strings.ToLower(strings.TrimSpace(path))}
	_ = s.writeLine("250 2.1.0 OK")
}

func (s *lmtpSession) handleRCPT(arg string) {
	if !s.txn.hasMail {
		_ = s.writeLine("503 5.5.1 need MAIL command")
		return
	}
	if s.txn.recipient != nil {
		_ = s.writeLine("452 4.5.3 too many recipients")
		return
	}
	path, ok := extractPathArg(arg, "TO:", false)
	if !ok {
		_ = s.writeLine("501 5.5.4 bad recipient syntax")
		return
	}
	snap := s.server.currentSnapshot()
	if snap == nil || snap.defaultAccountID == uuid.Nil {
		_ = s.writeLine("451 4.3.0 ingress cache unavailable")
		return
	}
	resolved, ok := snap.resolveRecipient(path)
	if !ok {
		_ = s.writeLine("550 5.1.1 unknown recipient domain")
		return
	}
	s.txn.recipient = &resolved
	_ = s.writeLine("250 2.1.5 OK")
}

func (s *lmtpSession) handleDATA() bool {
	if s.txn.recipient == nil {
		return s.writeLine("503 5.5.1 need RCPT command")
	}
	if !s.server.acquireSlot() {
		s.server.queueFull.Add(1)
		return s.writeLine("451 4.3.2 ingress queue full")
	}
	reserved := true
	defer func() {
		if reserved {
			s.server.slots <- struct{}{}
		}
	}()

	if !s.writeLine("354 End data with <CR><LF>.<CR><LF>") {
		return false
	}
	raw, oversized, err := s.readData()
	if err != nil {
		if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			log.Printf("[ingress] %s read DATA error: %v", s.remote, err)
		}
		return false
	}
	if oversized {
		s.server.oversizedMessages.Add(1)
		return s.writeLine("552 5.3.4 message exceeds ingress limit")
	}
	s.server.dataBytes.Add(uint64(len(raw)))

	jobCtx, cancel := context.WithTimeout(s.parent, s.server.cfg.DeliveryTimeout)
	defer cancel()
	resultCh := make(chan deliveryResult, 1)
	job := deliveryJob{
		ctx:            jobCtx,
		result:         resultCh,
		resolved:       *s.txn.recipient,
		envelopeSender: s.txn.mailFrom,
		raw:            raw,
	}
	if !s.server.submitJob(jobCtx, job) {
		s.server.jobsCancelled.Add(1)
		return s.writeLine("451 4.3.0 ingress shutting down")
	}
	reserved = false
	s.server.jobsSubmitted.Add(1)

	select {
	case result := <-resultCh:
		s.txn = transaction{}
		switch result.status {
		case deliveryOK:
			return s.writeLine("250 2.0.0 delivered")
		case deliveryPermFail:
			log.Printf("[ingress] permanent delivery failure: %v", result.err)
			return s.writeLine("550 5.1.1 recipient not deliverable")
		default:
			log.Printf("[ingress] temporary delivery failure: %v", result.err)
			return s.writeLine("451 4.3.0 temporary delivery failure")
		}
	case <-jobCtx.Done():
		s.server.deliveryTimeouts.Add(1)
		log.Printf("[ingress] delivery timeout for %s", job.resolved.address)
		return s.writeLine("451 4.3.0 delivery timeout")
	}
}

func (s *lmtpSession) writeCapabilities() bool {
	lines := []string{
		fmt.Sprintf("250-%s", s.server.cfg.Hostname),
		"250-8BITMIME",
		fmt.Sprintf("250-SIZE %d", s.server.cfg.BodyMaxBytes),
		"250 PIPELINING",
	}
	for _, line := range lines {
		if !s.writeRawLine(line) {
			return false
		}
	}
	return true
}

func (s *lmtpSession) writeLine(format string, args ...any) bool {
	return s.writeRawLine(fmt.Sprintf(format, args...))
}

func (s *lmtpSession) writeRawLine(line string) bool {
	_ = s.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := fmt.Fprintf(s.writer, "%s\r\n", line); err != nil {
		return false
	}
	return s.writer.Flush() == nil
}

func (s *lmtpSession) readCommandLine() (string, error) {
	_ = s.conn.SetReadDeadline(time.Now().Add(s.server.cfg.SessionTimeout))
	line, err := s.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	if len(line) > s.server.cfg.CommandMaxBytes {
		return "", fmt.Errorf("command line too long")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (s *lmtpSession) readData() ([]byte, bool, error) {
	var raw []byte
	oversized := false
	maxBytes := s.server.cfg.BodyMaxBytes
	for {
		_ = s.conn.SetReadDeadline(time.Now().Add(s.server.cfg.SessionTimeout))
		line, err := s.reader.ReadString('\n')
		if err != nil {
			return nil, oversized, err
		}
		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "." {
			break
		}
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}
		if len(raw)+len(line) > maxBytes {
			oversized = true
			continue
		}
		raw = append(raw, line...)
	}
	return raw, oversized, nil
}

func (snap *snapshot) resolveRecipient(address string) (resolvedRecipient, bool) {
	local, domain, ok := splitAddress(address)
	if !ok {
		return resolvedRecipient{}, false
	}

	// Walk DNS labels from most specific to least specific. This preserves
	// longest-root matching while changing lookup cost from O(active domains)
	// to O(labels in the recipient domain).
	matched, found := snap.domainsByName[domain]
	for candidate := domain; !found; {
		dot := strings.IndexByte(candidate, '.')
		if dot < 0 || dot == len(candidate)-1 {
			break
		}
		candidate = candidate[dot+1:]
		matched, found = snap.domainsByName[candidate]
	}
	if !found {
		return resolvedRecipient{}, false
	}
	return resolvedRecipient{
		address:           local + "@" + domain,
		localPart:         local,
		domainName:        domain,
		domainID:          matched.id,
		accountID:         snap.defaultAccountID,
		mailboxTTLMinutes: snap.mailboxTTLMinutes,
	}, true
}

func splitCommand(line string) (string, string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return "", ""
	}
	for i, r := range line {
		if r == ' ' || r == '\t' {
			return strings.ToUpper(line[:i]), strings.TrimSpace(line[i+1:])
		}
	}
	return strings.ToUpper(line), ""
}

func extractPathArg(arg, prefix string, allowEmpty bool) (string, bool) {
	arg = strings.TrimSpace(arg)
	if len(arg) < len(prefix) || !strings.EqualFold(arg[:len(prefix)], prefix) {
		return "", false
	}
	rest := strings.TrimSpace(arg[len(prefix):])
	var path string
	if strings.HasPrefix(rest, "<") {
		end := strings.Index(rest, ">")
		if end < 0 {
			return "", false
		}
		path = rest[1:end]
	} else {
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return "", false
		}
		path = strings.Trim(fields[0], "<>")
	}
	path = strings.TrimSpace(path)
	if path == "" && !allowEmpty {
		return "", false
	}
	return path, true
}

func splitAddress(address string) (string, string, bool) {
	address = strings.ToLower(strings.TrimSpace(address))
	address = strings.Trim(address, "<>")
	at := strings.LastIndex(address, "@")
	if at <= 0 || at == len(address)-1 {
		return "", "", false
	}
	local := strings.TrimSpace(address[:at])
	domain := strings.Trim(strings.TrimSpace(address[at+1:]), ".")
	if local == "" || domain == "" || strings.Contains(domain, "..") || strings.ContainsAny(domain, " \t\r\n") {
		return "", "", false
	}
	return local, domain, true
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
