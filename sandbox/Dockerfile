FROM ubuntu:24.04

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y \
    build-essential \
    procps \
    curl \
    file \
    git \
    locales \
    sudo \
    libpcap-dev \
    libgtest-dev \
    libgoogle-glog-dev \
    cmake \
    xxd \
    && rm -rf /var/lib/apt/lists/*

RUN localedef -i en_US -f UTF-8 en_US.UTF-8
ENV LANG=en_US.UTF-8
ENV LC_ALL=en_US.UTF-8

RUN useradd -m -s /bin/bash linuxbrew && \
    echo 'linuxbrew ALL=(ALL) NOPASSWD:ALL' >> /etc/sudoers

USER linuxbrew
WORKDIR /home/linuxbrew

ENV HOMEBREW_NO_AUTO_UPDATE=1
RUN /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

ENV PATH="/home/linuxbrew/.linuxbrew/bin:/home/linuxbrew/.linuxbrew/sbin:${PATH}"

# Build pkt2flow from source
RUN git clone https://github.com/caesar0301/pkt2flow.git /home/linuxbrew/pkt2flow && \
    cd /home/linuxbrew/pkt2flow && \
    mkdir build && cd build && \
    cmake .. -DCMAKE_BUILD_TYPE=Release && \
    make

RUN brew install \
    zeek \
    wireshark \
    uv \
    python@3.12 \
    gron \
    jq \
    tree

ENV VIRTUAL_ENV=/home/linuxbrew/venv
RUN uv venv $VIRTUAL_ENV

ENV PATH="$VIRTUAL_ENV/bin:$PATH"

RUN uv pip install \
    pyshark \
    scapy \
    pandas \
    ipython \
    requests \
    pytz \
    pycryptodome \
    cryptography \
    ecdsa

RUN git clone https://github.com/Pcapchu/pcapchu-scripts.git /home/linuxbrew/pcapchu-scripts && \
    uv pip install /home/linuxbrew/pcapchu-scripts/

WORKDIR /data

CMD ["/bin/bash"]