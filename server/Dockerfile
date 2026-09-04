FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/maccy-server ./cmd/server

FROM node:22-bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /opt/maccy-server
COPY package.json package-lock.json ./
# The worker is fixed to Model2Vec; zvec-grep's ONNX and llama.cpp backends are unused.
RUN npm ci --omit=dev --no-audit --no-fund \
    && rm -rf \
        node_modules/@huggingface/transformers \
        node_modules/@node-llama-cpp \
        node_modules/node-llama-cpp \
        node_modules/onnxruntime-common \
        node_modules/onnxruntime-node \
        node_modules/onnxruntime-web \
    && npm cache clean --force
COPY zvec_worker.mjs ./
COPY --from=build /out/maccy-server /usr/local/bin/maccy-server
RUN mkdir -p /opt/maccy-server/.zvec \
    && chown 65532:65532 /opt/maccy-server/.zvec
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/maccy-server"]
