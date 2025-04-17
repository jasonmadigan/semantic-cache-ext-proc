FROM golang:1.23.0-alpine AS build
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64

WORKDIR /src
COPY . .
RUN go mod download
RUN go build -o /semantic-cache-ext-proc

FROM registry.access.redhat.com/ubi8/ubi-minimal

WORKDIR /
COPY --from=build /semantic-cache-ext-proc /semantic-cache-ext-proc

ENTRYPOINT ["/semantic-cache-ext-proc"]
