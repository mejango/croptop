# croptop host in a container (Railway, Fly, any Docker host).
FROM golang:alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /croptop ./cmd/croptop

# libheif 1.23.6 fixes decoder security issues not yet covered by Alpine's
# packaged 1.23.0. Pin the official release and GitHub release-asset SHA-256.
FROM alpine:3.23 AS heif-build
RUN apk add --no-cache build-base cmake curl pkgconf libde265-dev libpng-dev
WORKDIR /build
RUN curl -fsSL https://github.com/strukturag/libheif/releases/download/v1.23.6/libheif-1.23.6.tar.gz -o libheif.tar.gz \
    && echo '4484346dc5995319dbc11e3a1c35d0a2ec46511ce370900869337fd2c7033125  libheif.tar.gz' | sha256sum -c - \
    && tar -xzf libheif.tar.gz
RUN cmake -S libheif-1.23.6 -B compiled \
    -DCMAKE_BUILD_TYPE=Release -DCMAKE_INSTALL_PREFIX=/usr/local \
    -DWITH_LIBDE265=ON -DWITH_LIBDE265_PLUGIN=OFF \
    -DWITH_X265=OFF -DWITH_X264=OFF -DWITH_OpenH264_DECODER=OFF \
    -DWITH_AOM_ENCODER=OFF -DWITH_AOM_DECODER=OFF -DWITH_LIBSHARPYUV=OFF \
    -DENABLE_PLUGIN_LOADING=OFF -DENABLE_PARALLEL_TILE_DECODING=OFF \
    -DWITH_GDK_PIXBUF=OFF -DWITH_EXAMPLE_HEIF_THUMB=OFF -DWITH_EXAMPLE_HEIF_VIEW=OFF \
    -DBUILD_TESTING=OFF -DBUILD_DOCUMENTATION=OFF \
    && cmake --build compiled --parallel 2 \
    && cmake --install compiled

FROM alpine:3.23 AS runtime
RUN apk add --no-cache ca-certificates libstdc++ libde265 libpng
COPY --from=heif-build /usr/local/bin/heif-dec /usr/local/bin/heif-dec
COPY --from=heif-build /usr/local/lib/libheif.so* /usr/local/lib/
RUN heif-dec --version
COPY --from=build /croptop /usr/local/bin/croptop
ENV CROPTOP_DOMAIN=crop.top CROPTOP_ROOT= CROPTOP_ANNOUNCE= CROPTOP_TRUST= CROPTOP_MOBILE_ORIGIN= CROPTOP_MOBILE_HOST=https://crop.top
EXPOSE 8090
# HTTP on 8090 for the proxy in front; 4001 is the swarm port to expose as raw TCP.
CMD ["sh", "-c", "exec croptop host --domain \"$CROPTOP_DOMAIN\" --root \"$CROPTOP_ROOT\" --announce \"$CROPTOP_ANNOUNCE\" --trust \"$CROPTOP_TRUST\" --listen 0.0.0.0:8090 --data /data"]

# CI/deploy gate: docker build --target mobile-media-test .
# Run the same compiled Go media tests against the production Linux decoder.
FROM build AS mobile-media-test-build
RUN CGO_ENABLED=0 go test -c -o /mobile-media.test ./internal/mobile

FROM runtime AS mobile-media-test
COPY --from=mobile-media-test-build /mobile-media.test /usr/local/bin/mobile-media.test
RUN /usr/local/bin/mobile-media.test -test.run 'Test(Normalize|HEIF|EXIF)' -test.v

FROM runtime AS final
