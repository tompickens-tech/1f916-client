FROM golang:1.23-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/client ./cmd/client
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12
COPY --from=build /out/client /client
COPY --from=build --chown=65532:65532 /out/data /data
USER 65532:65532
ENV LISTEN_ADDR=0.0.0.0:8080 DRAFTS_DIR=/data
EXPOSE 8080
ENTRYPOINT ["/client"]
