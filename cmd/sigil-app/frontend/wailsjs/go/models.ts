export namespace main {
	
	export class DailyHourlyRow {
	    date: string;
	    hours: number[];
	
	    static createFrom(source: any = {}) {
	        return new DailyHourlyRow(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.date = source["date"];
	        this.hours = source["hours"];
	    }
	}
	export class CategoryBreakdown {
	    category: string;
	    count: number;
	    acceptance_rate: number;
	
	    static createFrom(source: any = {}) {
	        return new CategoryBreakdown(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.category = source["category"];
	        this.count = source["count"];
	        this.acceptance_rate = source["acceptance_rate"];
	    }
	}
	export class DailyCount {
	    date: string;
	    total: number;
	    accepted: number;
	    dismissed: number;
	    pending: number;
	
	    static createFrom(source: any = {}) {
	        return new DailyCount(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.date = source["date"];
	        this.total = source["total"];
	        this.accepted = source["accepted"];
	        this.dismissed = source["dismissed"];
	        this.pending = source["pending"];
	    }
	}
	export class AnalyticsResult {
	    daily_counts: DailyCount[];
	    category_breakdown: CategoryBreakdown[];
	    hourly_distribution: number[];
	    daily_hourly: DailyHourlyRow[];
	    streak_days: number;
	
	    static createFrom(source: any = {}) {
	        return new AnalyticsResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.daily_counts = this.convertValues(source["daily_counts"], DailyCount);
	        this.category_breakdown = this.convertValues(source["category_breakdown"], CategoryBreakdown);
	        this.hourly_distribution = source["hourly_distribution"];
	        this.daily_hourly = this.convertValues(source["daily_hourly"], DailyHourlyRow);
	        this.streak_days = source["streak_days"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AskContext {
	    task?: string;
	    branch?: string;
	    recent_files?: string[];
	
	    static createFrom(source: any = {}) {
	        return new AskContext(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.task = source["task"];
	        this.branch = source["branch"];
	        this.recent_files = source["recent_files"];
	    }
	}
	
	export class CheckInitResult {
	    initialized: boolean;
	    config_path: string;
	
	    static createFrom(source: any = {}) {
	        return new CheckInitResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.initialized = source["initialized"];
	        this.config_path = source["config_path"];
	    }
	}
	export class CloudStatusResult {
	    connected: boolean;
	    tier: string;
	    sync_state: string;
	    ml_predictions_used: number;
	    llm_tokens_used: number;
	    llm_tokens_limit: number;
	
	    static createFrom(source: any = {}) {
	        return new CloudStatusResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connected = source["connected"];
	        this.tier = source["tier"];
	        this.sync_state = source["sync_state"];
	        this.ml_predictions_used = source["ml_predictions_used"];
	        this.llm_tokens_used = source["llm_tokens_used"];
	        this.llm_tokens_limit = source["llm_tokens_limit"];
	    }
	}
	
	
	export class DetectedEnvironment {
	    ides: string[];
	    tools: string[];
	    plugins: string[];
	
	    static createFrom(source: any = {}) {
	        return new DetectedEnvironment(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ides = source["ides"];
	        this.tools = source["tools"];
	        this.plugins = source["plugins"];
	    }
	}
	export class HealthAction {
	    label: string;
	    action: string;
	
	    static createFrom(source: any = {}) {
	        return new HealthAction(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.label = source["label"];
	        this.action = source["action"];
	    }
	}
	export class ServiceHealth {
	    name: string;
	    status: string;
	    message: string;
	    actions?: HealthAction[];
	
	    static createFrom(source: any = {}) {
	        return new ServiceHealth(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.status = source["status"];
	        this.message = source["message"];
	        this.actions = this.convertValues(source["actions"], HealthAction);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class HealthResult {
	    services: ServiceHealth[];
	
	    static createFrom(source: any = {}) {
	        return new HealthResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.services = this.convertValues(source["services"], ServiceHealth);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class InitConfig {
	    watch_dirs: string[];
	    inference_mode: string;
	    notification_level: number;
	    plugins: string[];
	    cloud_enabled: boolean;
	    cloud_provider: string;
	    cloud_api_key: string;
	    local_inference: boolean;
	    fleet_enabled: boolean;
	    fleet_endpoint: string;
	
	    static createFrom(source: any = {}) {
	        return new InitConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.watch_dirs = source["watch_dirs"];
	        this.inference_mode = source["inference_mode"];
	        this.notification_level = source["notification_level"];
	        this.plugins = source["plugins"];
	        this.cloud_enabled = source["cloud_enabled"];
	        this.cloud_provider = source["cloud_provider"];
	        this.cloud_api_key = source["cloud_api_key"];
	        this.local_inference = source["local_inference"];
	        this.fleet_enabled = source["fleet_enabled"];
	        this.fleet_endpoint = source["fleet_endpoint"];
	    }
	}
	
	export class TimelineEvent {
	    timestamp: string;
	    kind: string;
	    summary: string;
	    detail?: Record<string, any>;
	
	    static createFrom(source: any = {}) {
	        return new TimelineEvent(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.timestamp = source["timestamp"];
	        this.kind = source["kind"];
	        this.summary = source["summary"];
	        this.detail = source["detail"];
	    }
	}
	export class TimelineResult {
	    events: TimelineEvent[];
	    total: number;
	
	    static createFrom(source: any = {}) {
	        return new TimelineResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.events = this.convertValues(source["events"], TimelineEvent);
	        this.total = source["total"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class UpdateInfo {
	    version: string;
	    changelog: string;
	    url: string;
	    checksum: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.changelog = source["changelog"];
	        this.url = source["url"];
	        this.checksum = source["checksum"];
	    }
	}

}

