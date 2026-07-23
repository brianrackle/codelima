//go:build darwin && cgo

#import <Foundation/Foundation.h>
#import <objc/message.h>
#import <objc/runtime.h>

int codelima_nested_virtualization_supported(void) {
    Class platform = NSClassFromString(@"VZGenericPlatformConfiguration");
    SEL supported = NSSelectorFromString(@"isNestedVirtualizationSupported");
    if (platform == Nil || ![platform respondsToSelector:supported]) {
        return 0;
    }

    BOOL (*query)(id, SEL) = (BOOL (*)(id, SEL))objc_msgSend;
    return query(platform, supported) ? 1 : 0;
}
