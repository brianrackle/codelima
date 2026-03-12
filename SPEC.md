# CodeLima
An agentic coding framework which allows you to run codex, claude code, and other agents isolated in vms or containers with full privleges.

Fork, branch, merge work and remain in complete isolation using vms or docker containers.

## System

alias Environment = Literal["vm"] | Literal["container"] 

CodeLima Control Plane:

    class Resources:
        cpu_cores: int
        memory: int

    class Host:
        name: String
        id: UUID
        parent: Optional[Host]
        children: set[Host]
        environment: Environment 
        max-depth: int = 3
        context: string #logs from the chat

        def request_start(name: Optional[string], environment: Optional[Environment] = DefaultEnvironment, resources: Optional[Resources] = DefaultResources):
            """
                uses lima/colima to start the environment. The environment is initialized with the desired agent (e.g. codex-cli), a registration endpoints for parent and root hosts (to register new vms/containers with the root, and make requests to the parent like merge, restart, exit, report status, request resources) and codelima installed already registered with the parent and root hosts.
            """


        def request_clone():
            "uses the root registration endpoint to request a copy of the current host registered as a child"

        def start(parent: Optional[Host], name: Optional[string], environment: Optional[Environment] = DefaultEnvironment, resources: Optional[Resources] = DefaultResources):
            """ 
                uses lima/colima to start the environment. The environment is initialized with the desired agent (e.g. codex-cli), a registration endpoints for parent and root hosts (to register new vms/containers with the root, and make requests to the parent like merge, restart, exit, report status, request resources) and codelima installed already registered with the parent and root hosts.
                only works if host is root. Do we make it possible for all hosts to declare as roots? This would be beneficial if you have a VM host and want to spawn container children, or if you have a distributed setup where vms are spun up on metal rather than as nested vms.
            """


        def clone(parent: Optional[Host]):
       		"""
	 
    
    class ControlPlane:
        root: Host

Host:
    lima vm

