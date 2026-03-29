# **ADR-001: Identity and Access Management (IAM) Subsystem Architecture**

| Property | Value |
| :---- | :---- |
| **Author** | dr-dobermann |
| **Status** | Proposed |
| **Version** | 1.01 |
| **Date** | 2026-03-29 |

## **1\. Context and Problem Statement**

The gobpm engine is positioned as an embedded library (a set of packages) for executing BPMN 2.0 processes in Go. For the full implementation of User Tasks and API security, the engine requires an Identity and Access Management (IAM) subsystem.

The absence of a clear IAM strategy results in the inability to:

1. Route tasks to specific users (Assignee) or candidate groups (Candidate Groups).  
2. Restrict access to sensitive data (process variables, schemas).  
3. Isolate data of different clients in SaaS deployments.

Since gobpm is a library rather than a standalone monolithic application, hardcoding a custom database of users, passwords, and roles is an architectural anti-pattern.

## **2\. IAM System Requirements**

The IAM system in gobpm must satisfy the following requirements:

### **2.1. Functional Requirements**

* **Identification and Routing:** Ability to verify the existence of users and groups for correct User Task assignment.  
* **Granular Access Control (RBAC/ABAC):** Ability to configure access policies for:  
  * Process Library (reading/deploying models).  
  * Process Instances (starting, suspending, terminating).  
  * Tasks (claiming, completing, delegating).  
  * Data (restricting visibility of specific process variables based on the requester's role).  
* **Multi-tenancy:** Pervasive support for TenantID in all requests. Isolation of users, groups, and permissions within a specific tenant.

### **2.2. Architectural Requirements**

* **No Vendor Lock-in:** The engine must not dictate the account storage schema.  
* **"Batteries Included":** Availability of a lightweight implementation for testing and prototyping.

## **3\. Considered Alternatives**

### **Alternative 1: Built-in IAM Module (Custom DB)**

Implementing a full user management system within gobpm with its own Users, Groups, Roles, and Permissions tables, including password management methods.

* **Pros:** All-in-one solution, full control over data structure, easy to implement complex SQL queries for permission checks.  
* **Cons:** Violates the library paradigm. Forces users to migrate corporate directories (Active Directory, Keycloak) into the gobpm database. Increases security risks (need for password/hash storage audits).

### **Alternative 2: Strictly Token-based Verification (Stateless)**

The engine knows nothing about users. All API methods accept a JWT token. The engine validates the token (using a public key) and reads claims directly from it.

* **Pros:** Fully Stateless, no need for external DB calls, perfect scalability.  
* **Cons:** Impossible to implement searching and routing. If a BPMN process specifies "Assign task to group Managers," the engine cannot verify if such a group exists or retrieve a list of all managers to send notifications (as managers' tokens are not currently in the system).

### **Alternative 3: Ports and Adapters (Identity & Authorization Service Interfaces)**

Definition of two strict interfaces:

1. IdentityService (Read-only: user existence verification, retrieving user's groups).  
2. AuthorizationService (Permission check: "Does user X have permission for action Y on resource Z in tenant T?").  
   The gobpm core interacts only with these interfaces.  
* **Pros:** Maximum flexibility. Library users can write an adapter that calls a corporate gRPC auth service or reads directly from LDAP/Keycloak. The engine does not store passwords or manage authentication (login/password check) — it handles only authorization and metadata retrieval.  
* **Cons:** Developers must implement these interfaces for Production environments if their IdP differs from standard ones.

## **4\. Decision Outcome**

**Alternative 3: Ports and Adapters** is selected.

The IAM architecture in gobpm will be built on abstract interfaces, segregating responsibilities.

### **4.1. Solution Architecture and Action Taxonomy**

To ensure maximum compatibility with user codebases, external identifiers (TenantID, UserID, GroupID) are implemented as interfaces. This allows host applications to pass their native data types (e.g., uuid.UUID) directly into gobpm methods without conversion.

**Action Taxonomy:**

Per industry standards, the system must guarantee that a specific action can only be applied to the corresponding resource type. From a domain model perspective, resources constrain the set of actions, not vice versa. To keep the IAM core independent of BPMN specifics, this constraint is expressed strictly through the ResourceType interface. The ResourceType interface requires a SupportedActions() \[\]Action method. Resource types will be defined in respective domain packages (e.g., the task package defines TaskResource, supporting ClaimAction), while pkg/iam remains clean and extensible.

### **4.2. Segregation of Subjects and Resources**

Conceptually, it is critical not to mix GroupID (subject attribute) and Resource (authorization target).

* **Classic Segregation (Principal vs. Resource):** Always an *Actor* (IdentityContext) and a *Target* (Resource). Groups form the Actor context.  
* **Lifecycle Ownership:** gobpm manages its resources (instances, tasks, variables). It **does not manage** users or groups — they reside in external IdPs. Making a Group a Resource would require supporting actions like CreateGroup, violating the external identity concept.  
* **Identity Facts vs. Access Policies:** Checking group membership (member\_of) is an *identity fact* handled by the IdP. Using AuthorizationService.CheckAccess for this mixes responsibilities.

### **4.3. Terminology: Groups, Roles, and Permissions**

* **Groups vs. Roles:** BPMN uses Candidate Groups. For gobpm, GroupID is a universal container. If an IdP uses "Roles" for routing, the IdentityService adapter maps these Roles to GroupID.  
* **Permissions:** A Permission is a grant for an Action on a Resource. gobpm does **not load** user permissions into memory. It uses the delegation pattern (PEP/PDP): the engine calls CheckAccess(), and the adapter determines the result.

### **4.4. Dynamic Routing and Org Structure (Reporting Lines)**

Scenarios like *"Assign task to the initiator's manager"* are handled outside pkg/iam, delegating to the **Expression Engine**:

* **Mechanism:** The Assignee field in BPMN contains an expression: ${orgService.getManager(initiatorID)}.  
* **Connection:** The host application registers a custom function orgService.getManager in the expression engine, which calls an external HR service.

### **4.5. Tenant Membership Management**

gobpm **deliberately avoids tenant membership management** (no AddUserToTenant methods).

* **Management (IdM):** External IdP responsibility.  
* **Verification:** gobpm only *confirms* membership via IdentityService.VerifyUser(ctx, tenantID, userID).

### **4.6. Bootstrapping and Tenant Superuser**

The engine **deliberately lacks a hardcoded "Superuser" concept**. Every check is delegated to AuthorizationService.CheckAccess(). Superuser logic is implemented in the adapter (e.g., a specific technical group ID like tenant-admins always returning true).

### **4.7. Service Accounts and System Actors**

For internal triggers (timers, async continuations), IdentityContext includes an IsSystem() flag. This allows the AuthorizationService to automatically permit internal system calls.

### **4.8. Responsibility Boundary and Audit**

* **Authentication:** gobpm **does not** parse JWTs or manage sessions. Extracting data from requests is the responsibility of the host application's API gateway/middleware.  
* **Audit (Audit Trail):** Every event (e.g., TaskClaimedEvent) is linked to the UserID or system identifier from the IdentityContext, ensuring a legally significant and transparent audit trail.

### **4.9. "Batteries Included" Delivery**

The library includes:

1. **In-Memory Identity Provider (iammem):** Based on Go maps, loaded from YAML.  
2. **Default Authorization Provider (authmem):** Basic logic (Wildcard or admin group check).

## **5\. Standard Compliance**

The architecture aligns with the **BPMN 2.0** specification, which states that organizational structures are **outside the scope of BPMN**. gobpm simply consumes these external identity facts to resolve HumanPerformer and PotentialOwner assignments.

## **6\. Consequences**

* **Pros:** Implementation flexibility, zero-overhead DX (interface-based IDs), pervasive multi-tenancy, and security via strict action taxonomy.  
* **Cons:** Interface comparison complexity in Go maps (mitigated by .String() calls), potential overhead from external IdP calls (mitigated by caching in thresher).

# **Changes**

### **2026-03-29**

* Translated to English.  
* Version increased to 1.01.

### **2026-03-28**

* Metadata table and standardized header added.  
* Paths updated to pkg/iam.  
* Refined system actor definitions and audit boundaries.