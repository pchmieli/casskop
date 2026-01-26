# Duplicate PV UUID Issue - The REAL Problem!

## What's Happening

Even though we only have **ONE loop device** attached now, LVM is detecting **DUPLICATE PV UUIDs**!

### Evidence from Logs:
```
=== Checking for duplicate PV UUIDs ===
❌ DUPLICATE PV UUIDs DETECTED!

Loop devices attached to disk.img: 1
/dev/loop0: [0050]:3477519 (/mnt/disks/disk.img)
✅ VALIDATION PASSED: Only one loop device
```

**The validation passed for loop devices but FAILED for PV UUIDs!**

## Root Cause

When a loop device is detached and then re-attached to the same backing file, the **PV metadata persists on the file**. 

### What Happened:
1. **First run**: `/dev/loop0` created → PV created with UUID `xxx-yyy-zzz` → stored on disk.img
2. **Pod restart** (kubelet restart): `/dev/loop0` detached
3. **Second run**: `/dev/loop1` created → Attached to same disk.img (which still has PV metadata!)
4. **Now**: We cleaned up `/dev/loop1`, only `/dev/loop0` remains
5. **BUT**: The disk.img file **STILL HAS THE OLD PV METADATA** from when both devices existed!

### The Problem:
LVM sees the PV UUID on the current loop device AND remembers it from the previous device configuration. Even though only one loop device exists now, LVM's metadata cache still has the duplicate UUID reference.

## The Error This Causes

```
WARNING: Not using device /dev/loop1 for PV wKqYkH-9wHN-dVDI-efFp-75DG-FTKG-0jrbB1.
WARNING: PV wKqYkH-9wHN-dVDI-efFp-75DG-FTKG-0jrbB1 prefers device /dev/loop0 because device name matches previous.
Cannot update volume group lvmvg with duplicate PV devices
```

LVM refuses to work with the volume group because it thinks there are duplicate PVs!

## The Fix

We need to **wipe the PV metadata** before creating a new PV, to ensure we start fresh:

### Added to lvm-installer.yaml:
```bash
# Check for duplicate PV UUIDs
DUPLICATE_PV_UUIDS=$(pvs --noheadings -o pv_uuid 2>/dev/null | sort | uniq -d || true)

if [ -n "$DUPLICATE_PV_UUIDS" ]; then
  echo "⚠️  WARNING: Duplicate PV UUIDs detected!"
  echo "Cleaning up PV metadata..."
  
  # Wipe PV metadata from the loop device
  pvremove -ff "$LOOP_DEVICE" 2>/dev/null || true
  
  # Also wipe any stale filesystem signatures
  wipefs -a "$LOOP_DEVICE" 2>/dev/null || true
  
  echo "✅ Cleaned up duplicate PV metadata"
fi
```

### What This Does:
1. **Checks** for duplicate PV UUIDs using `pvs` command
2. **Removes** PV metadata using `pvremove -ff` (force remove)
3. **Wipes** any remaining signatures using `wipefs -a`
4. **Allows** fresh PV creation without UUID conflicts

## Updated Validation

The validation gate now checks **BOTH**:
1. ✅ Only one loop device exists
2. ✅ **No duplicate PV UUIDs exist** (NEW!)

If either check fails, the workflow stops immediately with a clear error message.

## Expected Behavior After Fix

### Before (Current - BROKEN):
```
Loop devices: 1 ✅
Duplicate PV UUIDs: YES ❌
Validation: PASSED (WRONG!)
OpenEBS: FAILS with "duplicate PV devices"
```

### After Fix:
```
Loop devices: 1 ✅
Found duplicate PV UUIDs
Wiping PV metadata from /dev/loop0...
✅ Cleaned up duplicate PV metadata
Creating fresh PV...
Creating VG...
Duplicate PV UUIDs: NO ✅
Validation: PASSED ✅
OpenEBS: SUCCESS ✅
```

## Why This Happens in k3d/Docker

In containerized environments like k3d:
- The backing file (`disk.img`) **persists** across pod restarts
- Loop devices are **ephemeral** (destroyed on restart)
- But the **PV metadata on disk.img remains**!
- When a new loop device attaches to the same file, LVM sees "duplicate" metadata

In bare metal, you'd typically:
- Have persistent loop device numbers
- OR manually wipe disks between tests
- OR use different backing files each time

## Commands to Verify

### Check for duplicate PV UUIDs:
```bash
pvs --noheadings -o pv_uuid | sort | uniq -d
# If output: Shows duplicate UUIDs
# If empty: No duplicates
```

### Check PV scan (shows what LVM sees):
```bash
pvscan
# Will show warnings about duplicate devices
```

### Wipe PV metadata:
```bash
pvremove -ff /dev/loop0
wipefs -a /dev/loop0
```

### Verify cleanup:
```bash
pvs -o pv_name,pv_uuid
# Should show only unique UUIDs
```

## Files Modified

1. **test/setup/lvm-installer.yaml** - Added PV metadata cleanup
2. **.github/workflows/e2e-tests.yml** - Enhanced validation to check PV UUIDs

## Summary

**The loop device fix worked!** Only one loop device exists now.  
**But** we discovered a second issue: duplicate PV UUIDs from stale metadata.  
**The new fix** wipes PV metadata before creating new PVs, ensuring no duplicates.

---

**Commit these changes and re-run the test!**
