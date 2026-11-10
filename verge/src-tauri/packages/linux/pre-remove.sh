#!/bin/bash
if [ -x /usr/bin/pixiu-service-uninstall ]; then
  /usr/bin/pixiu-service-uninstall || true
fi
