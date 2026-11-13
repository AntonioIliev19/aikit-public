//go:build darwin && cgo

package index

/*
#include <mach/mach.h>
#include <mach/mach_host.h>
#include <stdint.h>
static uint64_t conmesh_available_memory(void) {
 mach_port_t host = mach_host_self();
 vm_size_t page_size = 0;
 vm_statistics64_data_t stats;
 mach_msg_type_number_t count = HOST_VM_INFO64_COUNT;
 kern_return_t a = host_page_size(host, &page_size);
 kern_return_t b = host_statistics64(host, HOST_VM_INFO64, (host_info64_t)&stats, &count);
 mach_port_deallocate(mach_task_self(), host);
 if (a != KERN_SUCCESS || b != KERN_SUCCESS) return 0;
 // Conservative reclaimable estimate; excludes compressed and speculative pages.
 return ((uint64_t)stats.free_count + stats.inactive_count) * page_size;
}
*/
import "C"

func availableMemory() uint64 { return uint64(C.conmesh_available_memory()) }
