FROM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/tsconfig.json ./
COPY web/src ./src
RUN npm run build

FROM golang:1.27.3-bookworm AS build
RUN apt-get update && apt-get install -y --no-install-recommends cmake libssl-dev && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY third_party/moonlight-common-c third_party/moonlight-common-c
RUN cmake -S third_party/moonlight-common-c -B build/mcc -DBUILD_SHARED_LIBS=OFF -DCMAKE_BUILD_TYPE=Release \
    && cmake --build build/mcc --parallel 2
COPY bridge bridge
COPY internal internal
COPY web/*.html web/embed.go ./web/
COPY --from=web /src/web/dist web/dist
RUN go build -o /out/tack-bridge ./bridge/cmd/tack-bridge

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends libssl3 ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/tack-bridge /usr/local/bin/tack-bridge
USER 1000:1000
WORKDIR /srv/tack
STOPSIGNAL SIGINT
ENTRYPOINT ["tack-bridge"]
CMD ["serve"]
