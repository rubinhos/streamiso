# The player: Neko's VLC image, plus what Blu-ray menus need.
#
# Most Blu-ray menus are Java programs on the disc (BD-J). VLC runs them through libbluray, which needs
# its BD-J module and a Java runtime; without them VLC skips the menu and plays the film. Java 11 is used
# on purpose: with Debian's Java 21 or 25, libbluray 1.3.4 starts the disc's program but it stops at the
# first network check (BD-Live), before the menu. Temurin is the Eclipse Foundation's build of OpenJDK.

ARG NEKO_IMAGE=ghcr.io/m1k1o/neko/vlc:latest
FROM ${NEKO_IMAGE}

ARG TEMURIN_VERSION=11.0.33+10
ARG TARGETARCH

RUN set -eux; \
    apt-get update; \
    # libbluray-bin: bd_list_titles, which tells the launcher a Blu-ray's audio and subtitle languages
    apt-get install -y --no-install-recommends curl ca-certificates libbluray-bin; \
    # The BD-J module is two .jar files; installing the package would also pull Debian's Java 21.
    cd /tmp; \
    apt-get download libbluray-bdj; \
    dpkg-deb -x libbluray-bdj_*.deb /; \
    rm libbluray-bdj_*.deb; \
    ls /usr/share/java/libbluray-j2se-*.jar; \
    case "${TARGETARCH:-amd64}" in amd64) arch=x64 ;; arm64) arch=aarch64 ;; *) echo "unsupported: $TARGETARCH"; exit 1 ;; esac; \
    tag="jdk-$(echo "$TEMURIN_VERSION" | sed 's/+/%2B/')"; \
    file="OpenJDK11U-jre_${arch}_linux_hotspot_$(echo "$TEMURIN_VERSION" | tr + _).tar.gz"; \
    base="https://github.com/adoptium/temurin11-binaries/releases/download/$tag"; \
    cd /tmp; \
    curl -fsSLO "$base/$file"; \
    curl -fsSL "$base/$file.sha256.txt" | sha256sum -c -; \
    mkdir -p /opt/java; \
    tar -xzf "$file" -C /opt/java --strip-components=1; \
    rm "$file"; \
    apt-get purge -y --auto-remove curl; \
    rm -rf /var/lib/apt/lists/*; \
    /opt/java/bin/java -version; \
    # What the player tells the catalog (the player-info volume), written by the launcher as user neko
    mkdir -p /info; \
    chown neko:neko /info

# libbluray looks for Java here
ENV JAVA_HOME=/opt/java
