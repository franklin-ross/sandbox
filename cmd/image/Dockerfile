FROM debian:bookworm

ARG GO_VERSION=1.23.6
ARG TARGETARCH

ENV DEBIAN_FRONTEND=noninteractive
ENV LANG=C.UTF-8
ENV LC_ALL=C.UTF-8

# Base tools
RUN apt-get update && apt-get install -y \
    zsh curl wget git sudo \
    ripgrep jq fzf tmux less unzip \
    build-essential pkg-config libssl-dev \
    ca-certificates gnupg \
    iptables dnsutils iproute2 \
    chromium \
    python3 python3-pip python3-venv \
    ruby ruby-dev \
    && rm -rf /var/cache/apt/archives/* /var/lib/apt/lists/*

# Go (arch-aware)
RUN curl -fsSL https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:${PATH}"

# Task runner (https://taskfile.dev)
RUN sh -c "$(curl -fsSL https://taskfile.dev/install.sh)" -- -d -b /usr/local/bin

ENV CHROME_BIN=/usr/bin/chromium
ENV CHROMIUM_BIN=/usr/bin/chromium
ENV PUPPETEER_EXECUTABLE_PATH=/usr/bin/chromium
ENV PUPPETEER_SKIP_DOWNLOAD=true

# Non-root user (UID matches host so bind-mounted files have correct ownership)
ARG HOST_UID=1000
RUN useradd -m -s /bin/zsh -u ${HOST_UID} agent \
    && echo "agent ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers
USER agent

# Go workspace
ENV GOPATH="/home/agent/go"
ENV PATH="${GOPATH}/bin:${PATH}"
RUN mkdir -p /home/agent/go

# Oh My Zsh first (creates .zshrc that later install scripts can append to)
RUN sh -c "$(curl -fsSL https://raw.githubusercontent.com/ohmyzsh/ohmyzsh/master/tools/install.sh)" "" --unattended

# Rust
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
ENV PATH="/home/agent/.cargo/bin:${PATH}"

# Claude Code CLI
RUN curl -fsSL https://claude.ai/install.sh | bash
ENV PATH="/home/agent/.local/bin:${PATH}"

# nvm + Node.js + Yarn
ENV NVM_DIR="/home/agent/.nvm"
ENV COREPACK_ENABLE_AUTO_PIN=0
RUN curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.1/install.sh | bash \
    && . "$NVM_DIR/nvm.sh" \
    && nvm install --lts \
    && nvm alias default lts/* \
    && corepack enable \
    && corepack prepare yarn@stable --activate \
    && ln -s "$NVM_DIR/versions/node/$(node -v)" "$NVM_DIR/current"
ENV PATH="/home/agent/.nvm/current/bin:${PATH}"

# Firewall + entrypoint
USER root
COPY init-firewall.sh /opt/init-firewall.sh
COPY entrypoint.sh /opt/entrypoint.sh
RUN chmod +x /opt/init-firewall.sh /opt/entrypoint.sh

USER agent
RUN mkdir -p /home/agent/.claude

ENTRYPOINT ["/opt/entrypoint.sh"]
CMD ["sleep", "infinity"]
