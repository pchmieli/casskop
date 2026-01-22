# Pre-Push Checklist - Storage Upsize Fix

## Changes Ready to Push

### ✅ Core Fix
- [x] `test/setup/lvm-installer.yaml` - Fixed duplicate loop device handling
  - Checks for existing loop devices first
  - Cleans up duplicates automatically
  - Reuses existing device when possible

### ✅ Comprehensive Diagnostics Added
- [x] After LVM installer deployment
  - Loop device counting
  - Duplicate PV UUID detection
  - Volume group verification
  
- [x] Validation gate (fails fast on duplicates)
  - Checks for exactly 1 loop device
  - Exits immediately if duplicates found
  - Shows detailed diagnostics on failure

- [x] After OpenEBS installation
  - Node capacity and filesystem info
  - Loop device verification from multiple perspectives
  - OpenEBS LVM visibility checks
  - Duplicate PV detection from OpenEBS

- [x] Background monitoring
  - Pod status every 20 seconds
  - PVC and PV tracking
  - Node condition monitoring

- [x] Final dump on test completion
  - Complete LVM installer logs
  - Detailed loop device analysis
  - Complete OpenEBS logs
  - All events and states

### ✅ Documentation
- [x] `STORAGE_UPSIZE_FIX.md` - Technical explanation
- [x] `STORAGE_UPSIZE_LOG_GUIDE.md` - Log reading guide
- [x] `CHANGES_SUMMARY.md` - Complete changes overview
- [x] `test/setup/test-lvm-fix.sh` - Local test script (optional)

## Expected Behavior After Push

### 🎯 Success Path
1. LVM installer creates/reuses single loop device
2. No duplicate PV warnings in logs
3. Validation gate passes: `✅ VALIDATION PASSED: Only one loop device`
4. OpenEBS installs successfully
5. OpenEBS can see volume group
6. PVC provisions successfully
7. Cassandra pod starts running
8. Test passes

### 🚫 Failure Path (if fix doesn't work)
1. LVM installer creates duplicate loop devices (shouldn't happen)
2. Validation detects duplicates
3. **Workflow fails immediately** at validation gate with clear error:
   ```
   ❌ VALIDATION FAILED: Multiple loop devices detected!
   ❌❌❌ CRITICAL ERROR ❌❌❌
   ```
4. Detailed diagnostics show:
   - All loop devices
   - PV scan results
   - PV UUIDs
5. No time wasted on OpenEBS installation

## Key Log Sections to Monitor

After pushing, monitor these sections in GitHub Actions logs:

### 1. LVM Installer Logs (Step: "Setup LVM on k3d cluster")
Look for:
```
Using loop device: /dev/loop0  (or /dev/loop1, but consistent)
✅ LVM setup complete on k3d-mycluster-server-0
```

### 2. Initial Diagnostics
Look for:
```
Number of loop devices attached to disk.img: 1
✅ Only one loop device attached to disk.img
✅ No duplicate PV UUIDs
```

### 3. Validation Gate (MOST IMPORTANT)
Look for:
```
========================================"
VALIDATION: Checking for duplicate loop devices...
========================================
Loop devices attached to disk.img: 1
✅ VALIDATION PASSED: Only one loop device
========================================
```

If this fails, the fix didn't work and you'll see detailed diagnostics.

### 4. OpenEBS Installation
Look for:
```
pod/openebs-lvm-controller-0 condition met
daemonset "openebs-lvm-node" successfully rolled out
```

### 5. Final Diagnostics
Look for:
```
Verify OpenEBS can see LVM (from openebs-lvm-plugin container):
  VG    #PV #LV #SN Attr   VSize  VFree
  lvmvg   1   0   0 wz--n- <3.00g <3.00g
```

### 6. Test Execution
Look for:
```
cassandra-e2e-dc1-rack1-0   1/1     Running
data-cassandra-e2e-dc1-rack1-0   Bound
```

## What to Do If Test Fails

### If Validation Gate Fails
The validation gate will show exactly what went wrong:
1. Check how many loop devices were detected
2. Review lvm-installer main container logs
3. Check if the loop device cleanup logic executed
4. Look for any errors in the setup process

### If OpenEBS Provisioning Fails
Check the "Check LVM controller logs" section for:
```
E0120 15:42:45 lvm: could not create volume ... Cannot update volume group lvmvg with duplicate PV devices
```
This means duplicates weren't caught by validation (shouldn't happen).

### If Pod Fails to Schedule
Check events for:
- `FailedScheduling` - Could be disk pressure or PVC issue
- `ProvisioningFailed` - OpenEBS couldn't create volume

Then review the final diagnostics for:
- Node capacity
- PVC status
- PV status
- OpenEBS logs

## Confidence Level

### High Confidence Items ✅
- Duplicate loop device detection (multiple checks at different stages)
- Validation gate will catch issues before OpenEBS installation
- Comprehensive diagnostics will show exact failure point
- Log guide helps interpret results

### Medium Confidence Items ⚠️
- Loop device cleanup fix (should work, but only one way to find out)
- GitHub Actions environment might have quirks we haven't seen locally

## Recommended Approach

1. **Push changes** to your branch
2. **Trigger the workflow** (comment `/kuttl-tests` on PR or manual trigger)
3. **Monitor the validation gate** - this is the key checkpoint
4. **Review diagnostics** if it fails
5. **Iterate if needed** - we now have excellent visibility

## Files to Commit

```bash
git add test/setup/lvm-installer.yaml
git add .github/workflows/e2e-tests.yml
git add STORAGE_UPSIZE_FIX.md
git add STORAGE_UPSIZE_LOG_GUIDE.md
git add CHANGES_SUMMARY.md
git add test/setup/test-lvm-fix.sh
git add PRE_PUSH_CHECKLIST.md  # this file

git commit -m "Fix storage-upsize test: resolve duplicate loop device issue

- Fixed lvm-installer.yaml to detect and clean up duplicate loop devices
- Added validation gate to fail fast on duplicate detection
- Enhanced diagnostics at all stages for better troubleshooting
- Created comprehensive documentation and log reading guide

Closes #XXX"  # Replace XXX with issue number

git push
```

## Success Criteria

The fix is successful if:
- ✅ Validation gate passes
- ✅ No duplicate PV warnings in logs
- ✅ OpenEBS successfully provisions volume
- ✅ PVC becomes Bound
- ✅ Cassandra pod starts Running
- ✅ storage-upsize test passes

Good luck! 🚀
