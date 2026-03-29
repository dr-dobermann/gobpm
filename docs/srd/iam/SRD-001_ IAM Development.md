# **SRD-001: Development Specification for the IAM Package**

**Author:** dr-dobermann

**Status:** Proposed

**Version:** 1.01

**Date:** 2026-03-29

## **1\. Introduction**

This document describes the requirements and specifications for developing the Identity and Access Management package (gobpm/pkg/iam) for the embedded gobpm engine. The foundation is based on **ADR-001: IAM Architecture** (Ports & Adapters pattern, multi-tenancy, read-only identity).

## **2\. Goals and Business Requirements**

1. **Task Routing:** Enable calculating Assignee and Candidate Groups for User Tasks.  
2. **API Security:** Provide a Policy Enforcement Point (PEP) for engine resources (schemas, instances, tasks, data).  
3. **Multi-tenancy:** Ensure strict context isolation between different client tenants.  
4. **Developer Experience (DX):** Provide "batteries included" in-memory implementations for rapid prototyping.

## **3\. Architectural Principles**

* **Read-Only Identity:** No CRUD operations for users/groups within gobpm.  
* **Authorization Delegation (Default Deny):** The engine is the PEP; logic for resolving rights resides in the external AuthorizationService adapter.  
* **Zero-overhead Types:** Identifiers are interfaces to allow native host types.  
* **Collaboration Out-of-Scope:** Comments, attachments, and watchers are the responsibility of the host application, not the engine core.

## **4\. Core Interfaces (pkg/iam)**

The following abstractions must be exported for use by the thresher runtime:

package iam

import "context"

type TenantID interface { String() string }  
type UserID   interface { String() string }  
type GroupID  interface { String() string }  
type Action   interface { String() string }

type ResourceType interface {  
	String() string  
	SupportedActions() \[\]Action  
}

type IdentityContext interface {  
	TenantID() TenantID  
	UserID() UserID  
	GroupIDs() \[\]GroupID  
	IsSystem() bool  
}

type Resource interface {  
	Type() ResourceType  
	ID()   string  
}

type IdentityService interface {  
	VerifyUser(ctx context.Context, tid TenantID, uid UserID) (bool, error)  
	HasGroup(ctx context.Context, tid TenantID, uid UserID, gid GroupID) (bool, error)  
	GetUserGroups(ctx context.Context, tid TenantID, uid UserID) (\[\]GroupID, error)  
	GetUsersByGroup(ctx context.Context, tid TenantID, gid GroupID) (\[\]UserID, error)  
}

type AuthorizationService interface {  
	CheckAccess(ctx context.Context, id IdentityContext, act Action, res Resource) (bool, error)  
}

### **4.1. Configuration Pattern**

Use the **Functional Options Pattern** for configuring IAM services to ensure flexibility and backward compatibility.

// Example configuration structures  
type IdentityConfig struct {  
	logger   Logger  
	cacheTTL time.Duration  
	yamlPath string  
}

type IdentityOption func(\*IdentityConfig)

// WithUserCacheTTL sets TTL for identity fact caching  
func WithUserCacheTTL(ttl time.Duration) IdentityOption { ... }

## **5\. Taxonomy Specifications**

Resources and actions must use lowercase string values.

* **Category:** read, create\_definition, read\_definitions, manage, grant, revoke.  
* **ProcessDefinition:** read, deploy, instantiate, delete, share, own.  
* **ProcessInstance:** read, suspend, activate, cancel, grant.  
* **UserTask:** read, claim, complete, delegate, release, share.  
* **Variable:** read, write.

## **6\. Included Providers**

### **6.1. In-Memory Identity Provider (pkg/iam/iammem)**

* Uses Go maps with sync.RWMutex.  
* Supports WithYAMLSource(path string) for pre-loading state.

### **6.2. Default Authorization Provider (pkg/iam/authmem)**

* Supports a **Wildcard** mode (always true).  
* Supports **Admin-only** mode via WithAdminGroup(groupID).

## **7\. YAML Schema for iammem**

tenants:  
  \- id: "tenant-default"  
    groups:  
      \- id: "grp-admins"  
    users:  
      \- id: "usr-alice"  
        groups: \["grp-admins"\]

## **8\. Definition of Done (DoD)**

1. **Interface Fixity:** Contracts in pkg/iam/interfaces.go finalized.  
2. **Zero Dependencies:** No external imports beyond context.  
3. **Initialization Pattern:** Strict use of Functional Options.  
4. **Thread-Safety:** iammem verified with \-race flag.  
5. **Coverage:** Minimum 85% unit test coverage for iammem and authmem.  
6. **Documentation:** Full GoDoc comments in English.  
7. **Examples:** example\_test.go included for basic setup.

# **Changes**

### **2026-03-29**

* Translated to English.  
* Version increased to 1.01.

### **2026-03-28**

* Metadata and changelog added.  
* YAML configuration format approved for iammem.