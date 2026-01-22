# Storage Upsize Test Fix - Duplicate Loop Device Issue

## Problem Analysis

The `storage-upsize` kuttl test was failing in GitHub Actions with the error:
```
Cannot update volume group lvmvg with duplicate PV devices.
```

### Root Cause

The LVM installer was creating **duplicate loop devices** pointing to the same disk image file (`/mnt/disks/disk.img`):
- `/dev/loop0` - pointing to disk.img (PV UUID: `twwSEI-yjiO-0YaZ-J1Y2-BTu3-BeES-fo6pNU`)
- `/dev/loop1` - pointing to the same disk.img (same PV UUID)

When OpenEBS LVM plugin tried to create a logical volume, LVM detected duplicate PV devices with the same UUID and refused the operation:

```
W0120 15:42:45.437584 lvm: said into stderr: WARNING: Not using device /dev/loop1 for PV twwSEI-yjiO-0YaZ-J1Y2-BTu3-BeES-fo6pNU.
WARNING: PV twwSEI-yjiO-0YaZ-J1Y2-BTu3-BeES-fo6pNU prefers device /dev/loop0 because device name matches previous.
Cannot update volume group lvmvg with duplicate PV devices.
E0120 15:42:45.437630 lvm: could not create volume lvmvg/pvc-d0666591-dfd6-47b8-a966-0485b6e15866
```

### Why This Happened

The original code in `test/setup/lvm-installer.yaml` had this logic:

```bash
LOOP_DEVICE=$(losetup -f)
if ! losetup $LOOP_DEVICE /mnt/disks/disk.img 2>/dev/null; then
  echo "Loop device already set up or error occurred"
  LOOP_DEVICE=$(losetup -j /mnt/disks/disk.img | cut -d: -f1)
fi
```

The issue:
1. `losetup -f` finds the next free loop device (e.g., `/dev/loop1`)
2. If `losetup $LOOP_DEVICE /mnt/disks/disk.img` fails (because the image is already attached to `/dev/loop0`), it falls back to finding the existing device
3. However, in some cases both loop devices exist simultaneously, creating the duplicate PV issue

## Solution

Fixed `test/setup/lvm-installer.yaml` to:

1. **Check for existing loop devices first** before creating new ones:
   ```bash
   EXISTING_LOOPS=$(losetup -j /mnt/disks/disk.img | cut -d: -f1 || true)
   ```

2. **Detect and clean up duplicate loop devices**:
   ```bash
   if [ "$LOOP_COUNT" -gt 1 ]; then
     echo "WARNING: Multiple loop devices found for same image. Cleaning up duplicates..."
     # Keep only the first one, remove others
     FIRST_LOOP=$(echo "$EXISTING_LOOPS" | head -n1)
     echo "$EXISTING_LOOPS" | tail -n +2 | while read loop_dev; do
       echo "Detaching duplicate loop device: $loop_dev"
       losetup -d "$loop_dev" 2>/dev/null || true
     done
     LOOP_DEVICE="$FIRST_LOOP"
   fi
   ```

3. **Reuse existing loop device** if only one exists:
   ```bash
   LOOP_DEVICE="$EXISTING_LOOPS"
   echo "Reusing existing loop device: $LOOP_DEVICE"
   ```

4. **Create new loop device only when none exists**:
   ```bash
   LOOP_DEVICE=$(losetup -f)
   echo "Setting up new loop device: $LOOP_DEVICE"
   losetup $LOOP_DEVICE /mnt/disks/disk.img
   ```

## Verification

The LVM volume group should now show only one PV device:
```
VG    #PV #LV #SN Attr   VSize  VFree
lvmvg   1   0   0 wz--n- <3.00g <3.00g

PV         VG    Fmt  Attr PSize  PFree
/dev/loop0 lvmvg lvm2 a--  <3.00g <3.00g
```

Without the duplicate PV warnings, and OpenEBS should be able to provision volumes successfully.

## Files Modified

- `test/setup/lvm-installer.yaml` - Fixed duplicate loop device handling

## Additional Diagnostics Added

Enhanced `.github/workflows/e2e-tests.yml` with comprehensive diagnostics to detect duplicate loop device issues:

### After LVM Installer Deployment:
- **Loop device detection**: Lists all loop devices and specifically checks devices attached to disk.img
- **Duplicate detection**: Counts loop devices and flags if more than one is attached to the same image
- **Volume group verification**: Shows VGs, PVs with verbose output
- **PV scan**: Runs `pvscan` to show all detected physical volumes including duplicates
- **UUID check**: Lists all PV names and UUIDs to detect duplicates

### Before OpenEBS Installation - Validation Gate:
- **Critical validation step**: Fails the workflow immediately if duplicate loop devices are detected
- **Early failure**: Prevents wasting time on OpenEBS installation if the setup is already broken
- **Detailed diagnostics on failure**: Shows all loop devices, PV scan, and PV UUIDs

### After OpenEBS Installation:
- **Node filesystem information**: Shows disk usage and capacity
- **Loop device verification from node**: Checks loop devices directly from k3d node container
- **OpenEBS verification**: Confirms OpenEBS can see the LVM volume groups
- **Duplicate PV check from OpenEBS**: Runs `pvscan` from openebs-lvm-plugin container to see what OpenEBS detects

### During Test Execution (Background Monitoring):
- Monitors cassandra pod status every 20 seconds
- Tracks PVC and PV status
- Monitors node conditions (DiskPressure, MemoryPressure)
- Logs recent events

### Final Dump (on test completion or failure):
- Complete LVM installer logs (init and main containers)
- Detailed loop device analysis showing duplicates if present
- Complete OpenEBS LVM controller and node logs
- PV scan results from both lvm-installer and openebs-lvm-node perspectives
- All events and resource states

These diagnostics will help quickly identify:
1. **If the fix worked**: Only one loop device should be attached to disk.img
2. **Where it failed**: Early validation will catch issues before OpenEBS installation
3. **Why it failed**: Comprehensive logs show exact state of loop devices and PVs at each stage
