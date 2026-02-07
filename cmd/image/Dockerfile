FROM ubuntu:24.04

ARG NODE_VERSION=22
ARG GO_VERSION=1.23.6
ARG RUBY_VERSION=3.3
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
    python3 python3-pip python3-venv \
    ruby${RUBY_VERSION} ruby${RUBY_VERSION}-dev \
    && rm -rf /var/lib/apt/lists/*

# Node.js via NodeSource
RUN curl -fsSL https://deb.nodesource.com/setup_${NODE_VERSION}.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# Yarn
RUN corepack enable && corepack prepare yarn@stable --activate

# Go (arch-aware)
RUN curl -fsSL https://go.dev/dl/go${GO_VERSION}.linux-${TARGETARCH}.tar.gz | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:${PATH}"

# Claude Code CLI
RUN npm install -g @anthropic-ai/claude-code

# Non-root user
RUN useradd -m -s /bin/zsh agent \
    && echo "agent ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers

# Install Rust as agent user
USER agent
RUN curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y
ENV PATH="/home/agent/.cargo/bin:${PATH}"

# Go workspace
ENV GOPATH="/home/agent/go"
ENV PATH="${GOPATH}/bin:${PATH}"
RUN mkdir -p /home/agent/go

# Oh My Zsh
RUN sh -c "$(curl -fsSL https://raw.githubusercontent.com/ohmyzsh/ohmyzsh/master/tools/install.sh)" "" --unattended

# Workspace
USER root
RUN mkdir -p /workspace && chown agent:agent /workspace

# Firewall + entrypoint
COPY init-firewall.sh /opt/init-firewall.sh
COPY entrypoint.sh /opt/entrypoint.sh
RUN chmod +x /opt/init-firewall.sh /opt/entrypoint.sh

USER agent
WORKDIR /workspace

ENTRYPOINT ["/opt/entrypoint.sh"]
CMD ["sleep", "infinity"]
