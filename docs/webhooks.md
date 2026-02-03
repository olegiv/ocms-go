# Webhooks

oCMS webhooks allow you to receive real-time notifications when events occur in your CMS. This enables integration with external services, automation workflows, and custom applications.

## Overview

When an event occurs (like a page being published), oCMS sends an HTTP POST request to your configured endpoint with details about the event.

## Setting Up Webhooks

### Creating a Webhook

1. Navigate to **Admin > Config > Webhooks**
2. Click **New Webhook**
3. Configure:
   - **Name**: Descriptive name (e.g., "Production Deployment Hook")
   - **URL**: Your endpoint URL (must be HTTPS in production)
   - **Secret**: Auto-generated or custom secret for signature verification
   - **Events**: Select which events to subscribe to
   - **Headers**: Optional custom HTTP headers
   - **Active**: Enable/disable the webhook
4. Click **Save**

### Testing Webhooks

After creating a webhook:
1. Click **Test** on the webhook row
2. A test event is sent to your endpoint
3. Check the delivery status in the webhook details

For local testing, consider using:
- [webhook.site](https://webhook.site) - Free temporary endpoints
- [ngrok](https://ngrok.com) - Tunnel to localhost

## Available Events

| Event | Trigger |
|-------|---------|
| `page.created` | When a new page is created |
| `page.updated` | When a page is modified |
| `page.deleted` | When a page is deleted |
| `page.published` | When a page is published |
| `page.unpublished` | When a page is unpublished |
| `media.uploaded` | When media is uploaded |
| `media.deleted` | When media is deleted |
| `form.submitted` | When a form is submitted |
| `user.created` | When a new user is created |
| `user.deleted` | When a user is deleted |

## Payload Format

### Standard Payload Structure

```json
{
    "type": "page.published",
    "timestamp": "2024-01-15T10:30:00Z",
    "data": {
        "id": 123,
        "title": "My Page Title",
        "slug": "my-page-title",
        "status": "published",
        "author_id": 1,
        "author_email": "admin@example.com"
    }
}
```

### Page Events

```json
{
    "type": "page.created",
    "timestamp": "2024-01-15T10:30:00Z",
    "data": {
        "id": 123,
        "title": "New Page",
        "slug": "new-page",
        "status": "draft",
        "author_id": 1,
        "author_email": "admin@example.com",
        "language_id": 1
    }
}
```

### Media Events

```json
{
    "type": "media.uploaded",
    "timestamp": "2024-01-15T10:30:00Z",
    "data": {
        "id": 456,
        "uuid": "a1b2c3d4-e5f6-7890-abcd-ef1234567890",
        "filename": "photo.jpg",
        "mime_type": "image/jpeg",
        "size": 1048576,
        "uploader_id": 1
    }
}
```

### Form Submission Events

```json
{
    "type": "form.submitted",
    "timestamp": "2024-01-15T10:30:00Z",
    "data": {
        "form_id": 1,
        "form_name": "Contact Form",
        "form_slug": "contact",
        "submission_id": 789,
        "data": {
            "name": "John Doe",
            "email": "john@example.com",
            "message": "Hello!"
        },
        "submitted_at": "2024-01-15T10:30:00Z"
    }
}
```

## Security

### Signature Verification

Every webhook request includes an HMAC-SHA256 signature in the `X-Webhook-Signature` header. Verify this signature to ensure the request is from oCMS:

```go
package main

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "io"
    "net/http"
)

func verifySignature(r *http.Request, secret string) bool {
    signature := r.Header.Get("X-Webhook-Signature")
    if signature == "" {
        return false
    }

    body, _ := io.ReadAll(r.Body)

    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    expectedSig := hex.EncodeToString(mac.Sum(nil))

    return hmac.Equal([]byte(signature), []byte(expectedSig))
}
```

### Request Headers

oCMS includes these headers with every webhook request:

| Header | Description |
|--------|-------------|
| `Content-Type` | `application/json` |
| `User-Agent` | `oCMS/1.0` |
| `X-Webhook-Signature` | HMAC-SHA256 signature of the payload |
| `X-Webhook-Event` | Event type (e.g., `page.published`) |
| `X-Webhook-Delivery-ID` | Unique delivery identifier |

Plus any custom headers you configure.

### HTTPS Requirement

In production, always use HTTPS endpoints. oCMS will send webhooks to HTTP endpoints, but this is not recommended as payloads may contain sensitive data.

## Delivery and Retry

### Delivery Process

1. Event occurs in oCMS
2. Webhook delivery record is created
3. HTTP POST is sent to your endpoint
4. Response is recorded

### Success Criteria

A delivery is considered successful if your endpoint returns:
- HTTP 2xx status code

### Retry Logic

If delivery fails:
- **5xx errors**: Retried with exponential backoff
- **Timeout**: Retried with exponential backoff
- **429 Too Many Requests**: Retried with exponential backoff
- **4xx errors (except 408, 429)**: NOT retried (client error)

### Retry Schedule

| Attempt | Delay |
|---------|-------|
| 1 | Immediate |
| 2 | 1 minute |
| 3 | 2 minutes |
| 4 | 4 minutes |
| 5 | 8 minutes |
| Max | 5 attempts (then marked as dead) |

### Dead Letter Queue

After 5 failed attempts, deliveries are marked as "dead". These remain in the system for 30 days and can be:
- Viewed in the webhook deliveries page
- Manually retried (resets attempt count)

## Monitoring

### Delivery History

View delivery history for each webhook:
1. Go to **Admin > Config > Webhooks**
2. Click on a webhook name
3. View the **Deliveries** tab

Each delivery shows:
- Status (pending, delivered, failed, dead)
- Response code
- Response body (truncated)
- Attempt count
- Timestamps

### Webhook Health

The webhook list shows:
- Last 24h deliveries
- Success rate percentage
- Health indicator (green/yellow/red)

### Manual Retry

To retry a failed delivery:
1. Open the webhook's Deliveries tab
2. Find the failed delivery
3. Click **Retry**

## Event Debouncing

To prevent webhook floods from rapid updates, oCMS includes event debouncing:

- Events within a 1-second window are coalesced
- Only the latest event data is sent
- Maximum 5-second wait before dispatch

This means if a page is updated 10 times in 2 seconds, only 1 webhook is sent with the final state.

## Best Practices

1. **Always verify signatures**: Protect against spoofed requests
2. **Respond quickly**: Return 2xx immediately, process async
3. **Handle duplicates**: Webhooks may occasionally be sent twice
4. **Use idempotent operations**: Design handlers to be safely re-run
5. **Log payloads**: For debugging and audit trails
6. **Monitor failures**: Set up alerts for repeated failures
7. **Keep secrets secure**: Never expose webhook secrets

## Example: Deployment Webhook

Here's a complete example of a webhook handler in Go:

```go
package main

import (
    "crypto/hmac"
    "crypto/sha256"
    "encoding/hex"
    "encoding/json"
    "io"
    "log"
    "net/http"
    "os/exec"
)

var webhookSecret = "your-webhook-secret"

type WebhookPayload struct {
    Type      string          `json:"type"`
    Timestamp string          `json:"timestamp"`
    Data      json.RawMessage `json:"data"`
}

func main() {
    http.HandleFunc("/webhook", handleWebhook)
    log.Fatal(http.ListenAndServe(":3000", nil))
}

func handleWebhook(w http.ResponseWriter, r *http.Request) {
    // Read body
    body, err := io.ReadAll(r.Body)
    if err != nil {
        http.Error(w, "Bad request", http.StatusBadRequest)
        return
    }

    // Verify signature
    signature := r.Header.Get("X-Webhook-Signature")
    if !verifySignature(body, signature, webhookSecret) {
        http.Error(w, "Invalid signature", http.StatusUnauthorized)
        return
    }

    // Parse payload
    var payload WebhookPayload
    if err := json.Unmarshal(body, &payload); err != nil {
        http.Error(w, "Invalid JSON", http.StatusBadRequest)
        return
    }

    // Handle event
    switch payload.Type {
    case "page.published":
        go triggerDeployment()
    }

    w.WriteHeader(http.StatusOK)
}

func verifySignature(body []byte, signature, secret string) bool {
    mac := hmac.New(sha256.New, []byte(secret))
    mac.Write(body)
    expected := hex.EncodeToString(mac.Sum(nil))
    return hmac.Equal([]byte(signature), []byte(expected))
}

func triggerDeployment() {
    cmd := exec.Command("./deploy.sh")
    if err := cmd.Run(); err != nil {
        log.Printf("Deployment failed: %v", err)
    }
}
```
