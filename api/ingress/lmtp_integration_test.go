package ingress

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"farmail/store"

	"github.com/google/uuid"
)

func TestLMTPDeliveryIntegration(t *testing.T) {
	dsn := os.Getenv("FARMAIL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("FARMAIL_TEST_DATABASE_URL is not set")
	}

	testCtx, cancelTest := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelTest()
	database, err := store.New(testCtx, dsn)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	defer database.Close()

	domainName := "lmtp-" + uuid.NewString() + ".test"
	domain, err := database.AddDomain(testCtx, domainName)
	if err != nil {
		t.Fatalf("add ingress domain: %v", err)
	}
	address := "hot@" + domainName
	defer func() {
		_, _ = database.PurgeMailboxes(context.Background(), uuid.Nil, true, nil, address, domainName, false, false)
		_ = database.DeleteDomain(context.Background(), domain.ID)
	}()

	serverCtx, cancelServer := context.WithCancel(context.Background())
	server := NewServer(database, Config{
		Addr: "127.0.0.1:0", Hostname: "mail.test", BodyMaxBytes: 196 * 1024,
		StoreBodyMaxBytes: 128 * 1024, Workers: 4, QueueSize: 16,
		DeliveryTimeout: 5 * time.Second, CacheRefresh: time.Hour,
		SessionTimeout: 5 * time.Second, CommandMaxBytes: 8192,
	})
	if err := server.Start(serverCtx); err != nil {
		cancelServer()
		t.Fatalf("start ingress: %v", err)
	}
	defer func() {
		cancelServer()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown ingress: %v", err)
		}
	}()

	listenAddress := server.ln.Addr().String()
	messages := []struct {
		name string
		raw  string
	}{
		{
			name: "small",
			raw:  "From: sender@example.test\r\nSubject: Small code 123456\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nCode: 123456\r\n",
		},
		{
			name: "100kb",
			raw:  "From: sender@example.test\r\nSubject: Large message\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + strings.Repeat("payload-1234567890 ", 5700) + "\r\n",
		},
		{
			name: "multipart",
			raw: "From: sender@example.test\r\nSubject: Multipart 654321\r\nContent-Type: multipart/alternative; boundary=integration\r\n\r\n" +
				"--integration\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nCode: 654321\r\n" +
				"--integration\r\nContent-Type: text/html; charset=utf-8\r\n\r\n<b>Code: 654321</b>\r\n--integration--\r\n",
		},
		{
			name: "hot_mailbox_repeat",
			raw:  "From: sender@example.test\r\nSubject: Hot mailbox 778899\r\nContent-Type: text/plain\r\n\r\nOTP 778899\r\n",
		},
	}
	for _, message := range messages {
		t.Run(message.name, func(t *testing.T) {
			if err := deliverLMTP(listenAddress, address, message.raw); err != nil {
				t.Fatalf("deliver message: %v", err)
			}
		})
	}

	const concurrentMessages = 64
	semaphore := make(chan struct{}, 12)
	errorsFound := make(chan error, concurrentMessages)
	var wait sync.WaitGroup
	for index := 0; index < concurrentMessages; index++ {
		index := index
		wait.Add(1)
		go func() {
			defer wait.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			raw := fmt.Sprintf("From: load@example.test\r\nSubject: Concurrent %02d code 482910\r\nContent-Type: text/plain\r\n\r\nOTP 482910\r\n", index)
			if index%4 == 0 {
				raw = fmt.Sprintf("From: load@example.test\r\nSubject: Concurrent multipart %02d\r\nContent-Type: multipart/alternative; boundary=load\r\n\r\n--load\r\nContent-Type: text/plain\r\n\r\nCode 482910\r\n--load\r\nContent-Type: text/html\r\n\r\n<b>482910</b>\r\n--load--\r\n", index)
			}
			if err := deliverLMTP(listenAddress, address, raw); err != nil {
				errorsFound <- fmt.Errorf("message %d: %w", index, err)
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	if t.Failed() {
		return
	}

	snapshot, err := database.LoadIngressSnapshot(testCtx)
	if err != nil {
		t.Fatalf("load ingress snapshot: %v", err)
	}
	mailbox, err := database.LookupMailboxScoped(testCtx, snapshot.DefaultAccountID, true, nil, address)
	if err != nil {
		t.Fatalf("lookup delivered mailbox: %v", err)
	}
	emails, total, err := database.ListEmails(testCtx, mailbox.ID, snapshot.DefaultAccountID, nil, 1, 100)
	wantMessages := len(messages) + concurrentMessages
	if err != nil || total != wantMessages || len(emails) != wantMessages {
		t.Fatalf("delivered email page: total=%d len=%d err=%v", total, len(emails), err)
	}
	stats := server.Stats()
	if stats.JobsDelivered != uint64(wantMessages) || stats.QueueDepth != 0 || stats.InFlight != 0 || stats.ActiveWorkers != 0 {
		t.Fatalf("unexpected settled ingress stats: %+v", stats)
	}

	// A client disappearing in DATA must release its reserved slot rather than
	// pinning capacity until process shutdown.
	if err := disconnectDuringData(listenAddress, address); err != nil {
		t.Fatalf("disconnect fixture: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for server.Stats().InFlight != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := server.Stats().InFlight; got != 0 {
		t.Fatalf("in-flight slots after DATA disconnect = %d", got)
	}
}

func deliverLMTP(address, recipient, raw string) error {
	conn, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(conn)
	if _, err := readLMTPReply(reader); err != nil {
		return err
	}
	commands := []string{"LHLO client.test", "MAIL FROM:<sender@example.test>", "RCPT TO:<" + recipient + ">", "DATA"}
	for _, command := range commands {
		if _, err := fmt.Fprintf(conn, "%s\r\n", command); err != nil {
			return err
		}
		code, err := readLMTPReply(reader)
		if err != nil {
			return err
		}
		if command == "DATA" {
			if code != 354 {
				return fmt.Errorf("DATA reply = %d", code)
			}
		} else if code != 250 {
			return fmt.Errorf("%s reply = %d", command, code)
		}
	}
	if !strings.HasSuffix(raw, "\r\n") {
		raw += "\r\n"
	}
	if _, err := fmt.Fprintf(conn, "%s.\r\n", raw); err != nil {
		return err
	}
	code, err := readLMTPReply(reader)
	if err != nil {
		return err
	}
	if code != 250 {
		return fmt.Errorf("delivery reply = %d", code)
	}
	_, _ = fmt.Fprint(conn, "QUIT\r\n")
	_, _ = readLMTPReply(reader)
	return nil
}

func disconnectDuringData(address, recipient string) error {
	conn, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		return err
	}
	reader := bufio.NewReader(conn)
	if _, err := readLMTPReply(reader); err != nil {
		_ = conn.Close()
		return err
	}
	for _, command := range []string{"LHLO client.test", "MAIL FROM:<sender@example.test>", "RCPT TO:<" + recipient + ">", "DATA"} {
		if _, err := fmt.Fprintf(conn, "%s\r\n", command); err != nil {
			_ = conn.Close()
			return err
		}
		if _, err := readLMTPReply(reader); err != nil {
			_ = conn.Close()
			return err
		}
	}
	_, _ = fmt.Fprint(conn, "partial body without terminator")
	return conn.Close()
}

func readLMTPReply(reader *bufio.Reader) (int, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return 0, err
	}
	if len(line) < 4 {
		return 0, fmt.Errorf("short LMTP reply %q", line)
	}
	var code int
	if _, err := fmt.Sscanf(line[:3], "%d", &code); err != nil {
		return 0, fmt.Errorf("invalid LMTP reply %q", line)
	}
	if line[3] != '-' {
		return code, nil
	}
	prefix := line[:3] + " "
	for {
		line, err = reader.ReadString('\n')
		if err != nil {
			return 0, err
		}
		if strings.HasPrefix(line, prefix) {
			return code, nil
		}
	}
}
