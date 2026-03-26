# orama-agent Buildroot external package
# The agent binary is cross-compiled externally and copied into the rootfs
# by post_build.sh. This .mk is a placeholder for Buildroot's package system
# in case we want to integrate the Go build into Buildroot later.

ORAMA_AGENT_VERSION = 1.0.0
ORAMA_AGENT_SITE = $(TOPDIR)/../agent
ORAMA_AGENT_SITE_METHOD = local

define ORAMA_AGENT_BUILD_CMDS
	cd $(@D) && GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
		go build -o $(@D)/orama-agent ./cmd/orama-agent/
endef

define ORAMA_AGENT_INSTALL_TARGET_CMDS
	install -D -m 0755 $(@D)/orama-agent $(TARGET_DIR)/usr/bin/orama-agent
endef

$(eval $(generic-package))
