package middleware

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"

	"fusion-platform.io/fusion-forge/internal/config"
)

const (
	saTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	saCAPath    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	k8sAPIBase  = "https://kubernetes.default.svc"
)

// NewAuthMiddleware returns a Gin handler that validates Kubernetes service
// account bearer tokens using the cluster's TokenReview API.
//
// When cfg.AuthEnabled is false the middleware is a no-op and every request
// passes through, making it safe to use in local development.
func NewAuthMiddleware(cfg *config.Config) gin.HandlerFunc {
	if !cfg.AuthEnabled {
		return func(c *gin.Context) { c.Next() }
	}

	client, err := buildK8sClient()
	if err != nil {
		panic("fusion-forge: auth enabled but cannot build in-cluster K8s client: " + err.Error())
	}

	allowedSAs := make(map[string]bool, len(cfg.AuthAllowedSAs))
	for _, sa := range cfg.AuthAllowedSAs {
		allowedSAs[sa] = true
	}

	return func(c *gin.Context) {
		token := extractBearer(c)
		if token == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}

		username, err := reviewToken(c.Request.Context(), client, token, cfg.AuthAudience)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "token validation failed"})
			return
		}

		if len(allowedSAs) > 0 {
			sa := saFromUsername(username)
			if !allowedSAs[sa] {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "service account not permitted"})
				return
			}
		}

		c.Set("k8s-username", username)
		c.Next()
	}
}

// extractBearer parses the Authorization header and returns the bearer token.
func extractBearer(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return ""
	}
	return strings.TrimPrefix(h, "Bearer ")
}

// saFromUsername converts a K8s service account username of the form
// "system:serviceaccount:<namespace>:<name>" to "namespace/name".
func saFromUsername(username string) string {
	parts := strings.Split(username, ":")
	if len(parts) == 4 && parts[0] == "system" && parts[1] == "serviceaccount" {
		return parts[2] + "/" + parts[3]
	}
	return username
}

// buildK8sClient creates an HTTP client that trusts the in-cluster CA and
// reads its own service account token for authenticating to the API server.
func buildK8sClient() (*http.Client, error) {
	ca, err := os.ReadFile(saCAPath)
	if err != nil {
		return nil, fmt.Errorf("read cluster CA: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(ca) {
		return nil, fmt.Errorf("parse cluster CA certificate")
	}
	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}, nil
}

// tokenReview types for the authentication.k8s.io/v1 API.
type tokenReviewRequest struct {
	APIVersion string              `json:"apiVersion"`
	Kind       string              `json:"kind"`
	Spec       tokenReviewSpec     `json:"spec"`
}

type tokenReviewSpec struct {
	Token     string   `json:"token"`
	Audiences []string `json:"audiences,omitempty"`
}

type tokenReviewResponse struct {
	Status tokenReviewStatus `json:"status"`
}

type tokenReviewStatus struct {
	Authenticated bool             `json:"authenticated"`
	User          tokenReviewUser  `json:"user"`
	Error         string           `json:"error,omitempty"`
}

type tokenReviewUser struct {
	Username string `json:"username"`
}

// reviewToken calls the Kubernetes TokenReview API and returns the
// authenticated service account username on success.
func reviewToken(ctx context.Context, client *http.Client, callerToken, audience string) (string, error) {
	// Re-read own SA token on every call — kubelet may rotate it.
	ownToken, err := os.ReadFile(saTokenPath)
	if err != nil {
		return "", fmt.Errorf("read own SA token: %w", err)
	}

	spec := tokenReviewSpec{Token: callerToken}
	if audience != "" {
		spec.Audiences = []string{audience}
	}

	body, err := json.Marshal(tokenReviewRequest{
		APIVersion: "authentication.k8s.io/v1",
		Kind:       "TokenReview",
		Spec:       spec,
	})
	if err != nil {
		return "", fmt.Errorf("marshal token review request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		k8sAPIBase+"/apis/authentication.k8s.io/v1/tokenreviews",
		bytes.NewReader(body),
	)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+string(ownToken))

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call token review API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token review API returned %d: %s", resp.StatusCode, string(b))
	}

	var result tokenReviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode token review response: %w", err)
	}

	if !result.Status.Authenticated {
		msg := result.Status.Error
		if msg == "" {
			msg = "token not authenticated"
		}
		return "", fmt.Errorf("%s", msg)
	}

	return result.Status.User.Username, nil
}
