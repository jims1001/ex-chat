# Visitor HTTP session contract

## Bootstrap and resume

1. Send `POST /api/v1/widget/config?website_token=<public website token>` with `{}` to create a new anonymous visitor.
2. Store the returned `visitor_token` and `source_id`. Both fields appear at the top level and inside `data`; `contact` is the newly created profile.
3. Send the session in `X-Visitor-Session` on protected requests. `X-Visitor-Token`, `visitor_session` query, and `visitor_token` query are compatibility alternatives. Prefer headers to avoid placing bearer credentials in URLs.
4. Send the existing session to `POST /config` to resume the same identity and obtain a refreshed session. Session tokens currently expire after 72 hours.

Without an existing valid session, bootstrap always creates a fresh random server-generated source and anonymous contact. Client `source_id`, `contact_token`, `identifier`, email, and other profile hints cannot recover or link an existing contact. They are ignored during anonymous bootstrap. A lost or expired session requires a new anonymous identity; historical profile recovery or cross-contact identity linkage requires a separately authenticated server-side flow and is intentionally not implemented by these widget endpoints.

An explicitly supplied invalid, empty, expired, wrong-purpose, or wrong-inbox session fails closed. It cannot fall back to a raw source, another lower-priority credential, or anonymous bootstrap. Credential precedence is `visitor_session` query, `visitor_token` query, `X-Visitor-Session`, then `X-Visitor-Token`.

## Protected routes

Every contact, conversation, message, transcript, typing, last-seen, label, event, and custom-attribute widget route requires a signed visitor session. The source/contact/tenant must still match the persisted contact-inbox mapping. The signed identity is authoritative even when body/query source hints disagree.

Conversation and message access additionally requires that the resource belongs to the same contact and inbox. Message edits require the message's contact sender to match the signed visitor. Transcript delivery loads only public messages and checks ownership before invoking the mail service.

`GET /config` remains public and returns no contact profile or pubsub token without a valid session. `GET /campaigns` and `GET /inbox_members` remain public.

## Profile identification

`POST /contact`, `PATCH /contact`, and `POST /set_user` update only the session-owned profile. An identifier or email already owned by another contact is rejected rather than used to select or merge that contact.

All three identifier-writing routes accept `identifier_hash` and enforce the same configured inbox HMAC policy. If `HMACMandatory` is enabled, an identifier requires a valid HMAC-SHA256 proof made with the server-side inbox HMAC secret. A provided invalid hash always fails. A valid hash does not grant permission to replace the session identity with another contact.

## Attachment uploads

`POST /direct_uploads` requires the full visitor session. Negotiated `PUT`/`POST /direct_uploads/*key` uses its separate, short-lived, single-upload capability; this capability is not a visitor session and cannot access protected HTTP routes.

New widget files use `/uploads/widget/inbox_<id>/contact_<id>/<filename>` and are returned as escaped URLs. Their owner is the signed visitor or the upload capability's signed contact. Download authorization is handled separately; possession of a URL alone does not establish ownership.

## Regression checks

`internal/handler/widget_http_security_test.go` tests unsigned and invalid access in release, debug, and test modes; explicit-empty credential precedence; bootstrap identity takeover attempts; cross-contact conversation/message mutations; mandatory HMAC alternate routes; and local mocked transcript delivery with private-message exclusion. Existing contact/conversation lifecycle fixtures bootstrap first and use the returned session. No authentication behavior is relaxed for tests.
