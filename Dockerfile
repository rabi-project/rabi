# SPDX-License-Identifier: Apache-2.0
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/rabid ./cmd/rabid \
 && CGO_ENABLED=0 go build -trimpath -o /out/rabi ./cmd/rabi

FROM alpine:3.22
RUN apk add --no-cache ca-certificates wget
COPY --from=build /out/rabid /out/rabi /usr/local/bin/
ENTRYPOINT ["rabid"]
