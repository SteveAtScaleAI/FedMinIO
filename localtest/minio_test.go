// Package localtest provides an end-to-end integration test for FedMinIO.
//
// It builds the minio binary as a subprocess, starts it against a temp directory,
// and exercises core S3 operations using the minio-go client.
//
// Usage:
//
//	go test ./localtest/ -v -timeout 120s
//
// Environment variables:
//
//	MINIO_TEST_BINARY  path to pre-built minio binary (default: build on the fly)
package localtest_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

const (
	testAccessKey = "testadmin"
	testSecretKey = "testpassword"
	testBucket    = "fed-test-bucket"
	testRegion    = "us-east-1"
)

var (
	testEndpoint  string
	minioClient   *minio.Client
	serverProcess *exec.Cmd
	testDataDir   string
)

// TestMain sets up and tears down the MinIO server subprocess.
func TestMain(m *testing.M) {
	// Find or build the minio binary
	binary, err := getOrBuildBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: could not get minio binary: %v\n", err)
		os.Exit(1)
	}

	// Pick a free port
	port, err := freePort()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: could not find free port: %v\n", err)
		os.Exit(1)
	}
	testEndpoint = fmt.Sprintf("127.0.0.1:%d", port)

	// Create a temp data directory
	testDataDir, err = os.MkdirTemp("", "fedminio-test-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: could not create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(testDataDir)

	// Start the server
	serverProcess = exec.Command(binary,
		"server",
		"--address", testEndpoint,
		"--console-address", ":0", // random console port
		testDataDir,
	)
	serverProcess.Env = append(os.Environ(),
		"MINIO_ROOT_USER="+testAccessKey,
		"MINIO_ROOT_PASSWORD="+testSecretKey,
	)
	serverProcess.Stdout = os.Stdout
	serverProcess.Stderr = os.Stderr

	if err := serverProcess.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: could not start minio server: %v\n", err)
		os.Exit(1)
	}

	// Wait for health endpoint to respond
	if err := waitForServer(testEndpoint, 30*time.Second); err != nil {
		serverProcess.Process.Kill()
		fmt.Fprintf(os.Stderr, "FATAL: server did not become ready: %v\n", err)
		os.Exit(1)
	}

	// Build the S3 client
	minioClient, err = minio.New(testEndpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(testAccessKey, testSecretKey, ""),
		Secure: false,
	})
	if err != nil {
		serverProcess.Process.Kill()
		fmt.Fprintf(os.Stderr, "FATAL: could not create minio client: %v\n", err)
		os.Exit(1)
	}

	// Run tests
	code := m.Run()

	// Teardown
	serverProcess.Process.Signal(os.Interrupt)
	serverProcess.Wait()

	os.Exit(code)
}

// TestCreateBucket verifies bucket creation.
func TestCreateBucket(t *testing.T) {
	ctx := context.Background()
	err := minioClient.MakeBucket(ctx, testBucket, minio.MakeBucketOptions{Region: testRegion})
	if err != nil {
		t.Fatalf("MakeBucket failed: %v", err)
	}
	exists, err := minioClient.BucketExists(ctx, testBucket)
	if err != nil {
		t.Fatalf("BucketExists failed: %v", err)
	}
	if !exists {
		t.Fatal("bucket should exist after creation")
	}
	t.Logf("bucket %q created successfully", testBucket)
}

// TestPutGetObject verifies a small object round-trip.
func TestPutGetObject(t *testing.T) {
	ctx := context.Background()
	objectName := "small-object.bin"
	data := make([]byte, 64*1024) // 64KB
	if _, err := io.ReadFull(rand.Reader, data); err != nil {
		t.Fatalf("rand read: %v", err)
	}

	_, err := minioClient.PutObject(ctx, testBucket, objectName, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{ContentType: "application/octet-stream"})
	if err != nil {
		t.Fatalf("PutObject failed: %v", err)
	}

	obj, err := minioClient.GetObject(ctx, testBucket, objectName, minio.GetObjectOptions{})
	if err != nil {
		t.Fatalf("GetObject failed: %v", err)
	}
	defer obj.Close()

	got, err := io.ReadAll(obj)
	if err != nil {
		t.Fatalf("reading object body: %v", err)
	}
	if !bytes.Equal(data, got) {
		t.Fatalf("data mismatch: uploaded %d bytes, got %d bytes", len(data), len(got))
	}
	t.Logf("small object round-trip OK (%d bytes)", len(data))
}

// TestListObjects verifies that uploaded objects appear in bucket listing.
func TestListObjects(t *testing.T) {
	ctx := context.Background()
	objectCh := minioClient.ListObjects(ctx, testBucket, minio.ListObjectsOptions{Recursive: true})

	var found []string
	for obj := range objectCh {
		if obj.Err != nil {
			t.Fatalf("ListObjects error: %v", obj.Err)
		}
		found = append(found, obj.Key)
	}
	if len(found) == 0 {
		t.Fatal("expected at least one object in bucket")
	}
	t.Logf("listed %d object(s): %s", len(found), strings.Join(found, ", "))
}

// TestMultipartUpload verifies that a large (>5MB) object is uploaded correctly via multipart.
func TestMultipartUpload(t *testing.T) {
	ctx := context.Background()
	objectName := "large-object.bin"
	size := int64(7 * 1024 * 1024) // 7MB — exceeds minio-go's 5MB multipart threshold

	_, err := minioClient.PutObject(ctx, testBucket, objectName, io.LimitReader(rand.Reader, size), size,
		minio.PutObjectOptions{ContentType: "application/octet-stream"})
	if err != nil {
		t.Fatalf("PutObject (multipart) failed: %v", err)
	}

	stat, err := minioClient.StatObject(ctx, testBucket, objectName, minio.StatObjectOptions{})
	if err != nil {
		t.Fatalf("StatObject failed: %v", err)
	}
	if stat.Size != size {
		t.Fatalf("size mismatch: expected %d, got %d", size, stat.Size)
	}
	t.Logf("multipart upload OK (%d bytes)", stat.Size)
}

// TestDeleteObject verifies object deletion.
func TestDeleteObject(t *testing.T) {
	ctx := context.Background()
	objectName := "small-object.bin"

	err := minioClient.RemoveObject(ctx, testBucket, objectName, minio.RemoveObjectOptions{})
	if err != nil {
		t.Fatalf("RemoveObject failed: %v", err)
	}

	// Verify it's gone
	_, err = minioClient.StatObject(ctx, testBucket, objectName, minio.StatObjectOptions{})
	if err == nil {
		t.Fatal("expected error after deleting object, got nil")
	}
	errResp := minio.ToErrorResponse(err)
	if errResp.Code != "NoSuchKey" {
		t.Fatalf("expected NoSuchKey error, got: %v", err)
	}
	t.Logf("object %q deleted successfully", objectName)
}

// TestObjectLockWORM verifies Object Lock COMPLIANCE mode rejects deletion.
// This validates federal compliance requirement for immutable audit records.
func TestObjectLockWORM(t *testing.T) {
	ctx := context.Background()
	wormBucket := "fed-worm-bucket"

	// Create bucket with object locking enabled (also enables versioning)
	err := minioClient.MakeBucket(ctx, wormBucket, minio.MakeBucketOptions{
		Region:        testRegion,
		ObjectLocking: true,
	})
	if err != nil {
		t.Fatalf("MakeBucket with ObjectLocking failed: %v", err)
	}
	defer func() {
		// Best-effort cleanup — may fail if object is still locked
		minioClient.RemoveBucket(ctx, wormBucket)
	}()

	objectName := "worm-record.bin"
	data := []byte("immutable federal record")
	retentionDate := time.Now().UTC().Add(1 * time.Hour)

	info, err := minioClient.PutObject(ctx, wormBucket, objectName, bytes.NewReader(data), int64(len(data)),
		minio.PutObjectOptions{
			ContentType:     "application/octet-stream",
			Mode:            minio.Compliance,
			RetainUntilDate: retentionDate,
		})
	if err != nil {
		t.Fatalf("PutObject with COMPLIANCE retention failed: %v", err)
	}
	t.Logf("object placed under COMPLIANCE retention until %v (versionID: %s)", retentionDate, info.VersionID)

	// In a versioned bucket, deleting without a version ID only adds a delete marker.
	// To test WORM, we must attempt to delete the specific version — that is what
	// COMPLIANCE retention blocks.
	err = minioClient.RemoveObject(ctx, wormBucket, objectName, minio.RemoveObjectOptions{
		VersionID: info.VersionID,
	})
	if err == nil {
		t.Fatal("expected deletion of locked version to be rejected, got nil error")
	}
	t.Logf("deletion of locked version correctly rejected: %v", err)
}

// TestDeleteBucket cleans up the test bucket.
func TestDeleteBucket(t *testing.T) {
	ctx := context.Background()

	// Remove any remaining objects first
	objectCh := minioClient.ListObjects(ctx, testBucket, minio.ListObjectsOptions{Recursive: true})
	for obj := range objectCh {
		if obj.Err != nil {
			continue
		}
		minioClient.RemoveObject(ctx, testBucket, obj.Key, minio.RemoveObjectOptions{})
	}

	err := minioClient.RemoveBucket(ctx, testBucket)
	if err != nil {
		t.Fatalf("RemoveBucket failed: %v", err)
	}
	t.Logf("bucket %q removed successfully", testBucket)
}

// --- helpers ---

func getOrBuildBinary() (string, error) {
	// Allow override via environment
	if b := os.Getenv("MINIO_TEST_BINARY"); b != "" {
		return b, nil
	}

	// Build from source into a temp file
	out := filepath.Join(os.TempDir(), "minio-fedtest")
	repoRoot := findRepoRoot()
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = repoRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	fmt.Fprintf(os.Stderr, "Building minio binary from %s...\n", repoRoot)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("go build failed: %w", err)
	}
	return out, nil
}

func findRepoRoot() string {
	// Walk up from this file's directory to find go.mod
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "."
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitForServer(endpoint string, timeout time.Duration) error {
	healthURL := fmt.Sprintf("http://%s/minio/health/live", endpoint)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("server at %s did not become healthy within %s", endpoint, timeout)
}
