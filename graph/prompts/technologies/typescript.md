# TYPESCRIPT / JAVASCRIPT EXTRACTION HINTS

## Receiver classification

A method call is architectural ONLY if the receiver is:
- an injected service/provider (from constructor)
- an imported API client
- an ORM/repository/database client
- an event/message producer
- an HTTP client (fetch, axios, got, ky)
- a generated API client

REJECT calls on: arrays, strings, dates, promises, console/logger, local helpers, validators/mappers.

## HTTP calls

Recognize these HTTP clients when URL and method are visible:
- `fetch(url)`, `axios.get(url)`, `got.post(url)`, `ky.get(url)`

Relative URLs => `calls_endpoint`
Absolute external URLs => `http_call`

## Type annotations vs runtime usage

TypeScript type annotations (`: User`, `as Order`) are NOT `uses_model`.
Only emit `uses_model` when the code actually queries/persists data through an ORM or database client.
