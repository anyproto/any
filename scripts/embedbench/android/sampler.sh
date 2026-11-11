#!/system/bin/sh
# 1 Hz: unix time, battery current (raw parcel from IBatteryPropertiesRegistrar.getProperty(CURRENT_NOW)), voltage mV
while true; do
  echo "$(date +%s) $(service call batteryproperties 1 i32 2 | tr -d '\n' | sed 's/.*0x00000000: //; s/ .\{0,2\}$//' | cut -c1-40) mv=$(dumpsys battery | grep '^  voltage:' | sed 's/.*: //')"
  sleep 1
  [ -f /data/local/tmp/eg2/stop_sampler ] && break
done
