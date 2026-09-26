FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/garmin ./cmd/garmin
RUN mkdir /data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/garmin /usr/local/bin/garmin
# A named volume mounted here inherits this ownership, so the non-root user can
# write the token file.
COPY --from=build --chown=nonroot:nonroot /data /data
ENV GARMIN_TOKEN_PATH=/data/tokens.json
ENTRYPOINT ["/usr/local/bin/garmin"]
