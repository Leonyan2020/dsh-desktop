export namespace process {
	
	export class Status {
	    running: boolean;
	    pid: number;
	    port: number;
	    version: string;
	    runtime: string;
	    url: string;
	    listening: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Status(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.pid = source["pid"];
	        this.port = source["port"];
	        this.version = source["version"];
	        this.runtime = source["runtime"];
	        this.url = source["url"];
	        this.listening = source["listening"];
	        this.message = source["message"];
	    }
	}

}

export namespace rtmgr {
	
	export class ReadyStatus {
	    hasNode: boolean;
	    nodeOK: boolean;
	    hasPnpm: boolean;
	    hasRuntime: boolean;
	    nodePath: string;
	    pnpmPath: string;
	    activeVersion: string;
	    needsSetup: boolean;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new ReadyStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hasNode = source["hasNode"];
	        this.nodeOK = source["nodeOK"];
	        this.hasPnpm = source["hasPnpm"];
	        this.hasRuntime = source["hasRuntime"];
	        this.nodePath = source["nodePath"];
	        this.pnpmPath = source["pnpmPath"];
	        this.activeVersion = source["activeVersion"];
	        this.needsSetup = source["needsSetup"];
	        this.message = source["message"];
	    }
	}
	export class VersionInfo {
	    version: string;
	    installed: boolean;
	    active: boolean;
	    legacy: boolean;
	    path: string;
	    tag: string;
	
	    static createFrom(source: any = {}) {
	        return new VersionInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.installed = source["installed"];
	        this.active = source["active"];
	        this.legacy = source["legacy"];
	        this.path = source["path"];
	        this.tag = source["tag"];
	    }
	}

}

