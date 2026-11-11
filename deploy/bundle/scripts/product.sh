# Product settings for the shared upgrade, backup and restore scripts.
PRODUCT=taktaplus
SVC=api                                   # compose service of the panel
BIN=taktaplus                             # its command inside the container
CDATA=/var/lib/taktaplus                  # data directory inside the container
HEALTH_URL=https://127.0.0.1:8443/healthz # checked from inside the container
ENV_PREFIX=TAKTAPLUS                      # .env names: ${ENV_PREFIX}_VERSION, _MASTER_KEY
