# Webhooks

oCMS webhooks allow you to receive real-time notifications when events occur in your CMS. This enables integration with external services, automation workflows, and custom applications.

## Overview

When an event occurs (like a page being published), oCMS sends an HTTP POST request to your configured endpoint with details about the event.

## Setting Up Webhooks

### Creating a Webhook

1. Navigate to **Admin > Webhooks** (administrators only)
2. Click **New Webhook**
3. Configure:
   - **Name**: Descriptive name (e.g., "Production Deployment Hook"), up to 255 characters
   - **URL**: Your endpoint URL (HTTPS only when `OCMS_REQUIRE_HTTPS_OUTBOUND` is on, which is the default in production; see [Destination Rules](#destination-rules))
   - **Secret**: Auto-generated or custom secret for signature verification
   - **Events**: Select which events to subscribe to
   - **Headers**: Optional custom HTTP headers (at most 20; names up to 64 characters, values up to 1024; `Host`, `Content-Length`, `Connection` and other hop-by-hop headers are not allowed)
   - **Active**: Enable/disable the webhook
4. Click **Save**

### Testing Webhooks

After creating a webhook:
1. Click **Test** on the webhook row
2. A `test` event is queued for your endpoint (its payload carries a message and the user who triggered it); the retry worker picks it up, so it can take up to 30 seconds to arrive
3. Check the delivery status on the webhook's delivery history page

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

Events are dispatched by the admin UI handlers (pages, media, users and public form submissions), by the REST API for page and media writes (`page.created`, `page.updated`, `page.deleted`, `media.uploaded`, `media.deleted`), and by scheduled publishing (`page.published`). Creating or editing a page, a status change in the edit form or the REST API included, sends only `page.created` or `page.updated`; `page.published` comes from the publish toggle and scheduled publishing, `page.unpublished` only from the publish toggle. Restoring a page version, creating a translation and content imports send no events. `author_email` is the email of the user who made the change, which can differ from `author_id`'s user; events from the REST API and the scheduler carry none.

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
        "language_code": "en"
    }
}
```

`language_code` is omitted when the page has none, and `published_at` (RFC 3339) is included once the page has been published.

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
        "data_mode": "full",
        "submitted_at": "2024-01-15T10:30:00Z"
    }
}
```

What `data` contains is set by `OCMS_WEBHOOK_FORM_DATA_MODE`: `redacted` (the default) sends the fields with sensitive names (password, token and similar) replaced by `[REDACTED]`, `none` omits `data`, and `full` sends every field. Values are cut at 1024 bytes. The example above shows `full` mode.

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

### Destination Rules

- **HTTPS**: when `OCMS_REQUIRE_HTTPS_OUTBOUND` is on (the default in production), only `https` URLs are accepted. Otherwise `http` URLs are accepted too, which is not recommended because payloads may contain sensitive data.
- **No internal targets**: `localhost` and URLs that resolve to private, loopback, link-local or other reserved addresses are rejected. The check runs when the webhook is saved, again before every delivery, and on every redirect (at most 10).
- **Host allowlist**: `OCMS_WEBHOOK_ALLOWED_HOSTS` (comma-separated exact hostnames) restricts destinations. In production, `OCMS_REQUIRE_WEBHOOK_ALLOWED_HOSTS` (on by default) refuses startup when active webhooks exist and no allowlist is configured.

## Delivery and Retry

### Delivery Process

1. Event occurs in oCMS (after debouncing, see below)
2. A delivery record is created for each active webhook subscribed to the event
3. HTTP POST is sent to your endpoint (30-second timeout)
4. Response is recorded (body truncated to 10 KB)

A retry worker checks for deliveries that are due every 30 seconds, so a retry can run up to 30 seconds after its scheduled delay.

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

After 5 failed attempts, deliveries are marked as "dead". Delivered and dead deliveries are deleted after 30 days; until then dead ones can be:
- Viewed in the webhook deliveries page
- Manually retried (resets attempt count)

A pending retry whose webhook has been disabled in the meantime is marked dead with the error "webhook disabled".

## Monitoring

### Delivery History

View delivery history for each webhook:
1. Go to **Admin > Webhooks**
2. Click the **View Deliveries** icon on the webhook row
3. Browse the **Delivery History** page (25 deliveries per page)

Each delivery shows:
- Status (pending, delivered, or dead, which the UI labels "Failed")
- Response code
- Response body (truncated)
- Attempt count
- Timestamps

### Webhook Health

The webhook list shows:
- Last 24h deliveries
- Success rate percentage (all deliveries)
- Health indicator: green at 95% or more, yellow from 80% up to 95%, red below 80%, unknown when there are no deliveries

### Manual Retry

To retry a failed delivery:
1. Open the webhook's delivery history page
2. Find the failed delivery
3. Click **Retry** (the delivery is reset to pending and picked up by the retry worker within 30 seconds)

## Event Debouncing

To prevent webhook floods from rapid updates, oCMS includes event debouncing:

- Events for the same type and entity ID within a 1-second window are coalesced
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
