FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/maccy-server ./cmd/server

ARG ZVEC_GO_VERSION=v0.7.0
RUN mkdir -p /out/zvec-lib \
    && for attempt in 1 2 3; do \
        go run github.com/zvec-ai/zvec-go/cmd/download-libs@${ZVEC_GO_VERSION} \
            -version ${ZVEC_GO_VERSION} \
            -dest /out/zvec-lib && break; \
        if [ "${attempt}" -eq 3 ]; then exit 1; fi; \
        sleep 2; \
    done \
    && find /out/zvec-lib -type f -name libzvec_c_api.so -exec cp {} /out/libzvec_c_api.so \;
ADD https://raw.githubusercontent.com/yanyiwu/cppjieba/v5.6.7/dict/jieba.dict.utf8 /out/jieba_dict/jieba.dict.utf8
ADD https://raw.githubusercontent.com/yanyiwu/cppjieba/v5.6.7/dict/hmm_model.utf8 /out/jieba_dict/hmm_model.utf8
RUN echo "6f7d4350e8861ef4139b2e3a6fad05430c19ae71f4b8378190edecac8aae2e6a  /out/jieba_dict/jieba.dict.utf8" | sha256sum -c - \
    && echo "f17790586ac86dd048c8adffed052c4bd2b28ed0682972c1275e59040c0589a7  /out/jieba_dict/hmm_model.utf8" | sha256sum -c -
RUN chmod 0644 /out/jieba_dict/*.utf8

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /opt/maccy-server
COPY --from=build /out/maccy-server /usr/local/bin/maccy-server
COPY --from=build /out/libzvec_c_api.so /usr/local/lib/libzvec_c_api.so
COPY --from=build /out/jieba_dict /usr/local/share/zvec/jieba_dict
ENV ZVEC_JIEBA_DICT_DIR=/usr/local/share/zvec/jieba_dict \
    ZVEC_LIBRARY_PATH=/usr/local/lib/libzvec_c_api.so
RUN mkdir -p /opt/maccy-server/.zvec \
    && chown 65532:65532 /opt/maccy-server/.zvec
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/maccy-server"]
