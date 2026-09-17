export namespace app {

	export class CancelledOccurrenceDTO {
	    original_date: string;
	    snapshot_present: boolean;
	    start_unix?: number;
	    end_unix?: number;
	    title?: string;
	    notes?: string;

	    static createFrom(source: any = {}) {
	        return new CancelledOccurrenceDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.original_date = source["original_date"];
	        this.snapshot_present = source["snapshot_present"];
	        this.start_unix = source["start_unix"];
	        this.end_unix = source["end_unix"];
	        this.title = source["title"];
	        this.notes = source["notes"];
	    }
	}
	export class ConflictDTO {
	    target: OccurrenceIdentityDTO;
	    other: OccurrenceIdentityDTO;
	    same_group: boolean;
	    overlap_unix: number;

	    static createFrom(source: any = {}) {
	        return new ConflictDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.target = this.convertValues(source["target"], OccurrenceIdentityDTO);
	        this.other = this.convertValues(source["other"], OccurrenceIdentityDTO);
	        this.same_group = source["same_group"];
	        this.overlap_unix = source["overlap_unix"];
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
	export class OccurrenceIdentityDTO {
	    schedule_id: number;
	    original_date: string;
	    title: string;
	    start_unix: number;
	    end_unix: number;

	    static createFrom(source: any = {}) {
	        return new OccurrenceIdentityDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schedule_id = source["schedule_id"];
	        this.original_date = source["original_date"];
	        this.title = source["title"];
	        this.start_unix = source["start_unix"];
	        this.end_unix = source["end_unix"];
	    }
	}
	export class ConflictCheckDTO {
	    status: string;
	    checked: boolean;
	    reason: string;
	    target?: OccurrenceIdentityDTO;
	    batch_unix: number;
	    occurrence_count: number;
	    same_schedule_only: boolean;
	    conflicts: ConflictDTO[];

	    static createFrom(source: any = {}) {
	        return new ConflictCheckDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.checked = source["checked"];
	        this.reason = source["reason"];
	        this.target = this.convertValues(source["target"], OccurrenceIdentityDTO);
	        this.batch_unix = source["batch_unix"];
	        this.occurrence_count = source["occurrence_count"];
	        this.same_schedule_only = source["same_schedule_only"];
	        this.conflicts = this.convertValues(source["conflicts"], ConflictDTO);
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

	export class ListSchedulesRequest {
	    limit: number;
	    cursor: string;

	    static createFrom(source: any = {}) {
	        return new ListSchedulesRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.limit = source["limit"];
	        this.cursor = source["cursor"];
	    }
	}
	export class ScheduleDTO {
	    id: number;
	    title: string;
	    notes: string;
	    cron?: string;
	    until_date_local?: string;
	    month_end_policy?: string;
	    open: boolean;
	    first_start_local: string;
	    duration_minutes: number;
	    revision: number;
	    preserved_override_count: number;
	    cancelled_occurrences: CancelledOccurrenceDTO[];
	    conflict?: ConflictCheckDTO;
	    // Go type: time
	    created_at_utc: any;
	    // Go type: time
	    updated_at_utc: any;

	    static createFrom(source: any = {}) {
	        return new ScheduleDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.notes = source["notes"];
	        this.cron = source["cron"];
	        this.until_date_local = source["until_date_local"];
	        this.month_end_policy = source["month_end_policy"];
	        this.open = source["open"];
	        this.first_start_local = source["first_start_local"];
	        this.duration_minutes = source["duration_minutes"];
	        this.revision = source["revision"];
	        this.preserved_override_count = source["preserved_override_count"];
	        this.cancelled_occurrences = this.convertValues(source["cancelled_occurrences"], CancelledOccurrenceDTO);
	        this.conflict = this.convertValues(source["conflict"], ConflictCheckDTO);
	        this.created_at_utc = this.convertValues(source["created_at_utc"], null);
	        this.updated_at_utc = this.convertValues(source["updated_at_utc"], null);
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
	export class ListSchedulesResponse {
	    items: ScheduleDTO[];
	    next_cursor: string;

	    static createFrom(source: any = {}) {
	        return new ListSchedulesResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.items = this.convertValues(source["items"], ScheduleDTO);
	        this.next_cursor = source["next_cursor"];
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


	export class SchedulerStatus {
	    available: boolean;
	    last_error: string;
	    epoch: number;
	    state: string;

	    static createFrom(source: any = {}) {
	        return new SchedulerStatus(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.available = source["available"];
	        this.last_error = source["last_error"];
	        this.epoch = source["epoch"];
	        this.state = source["state"];
	    }
	}
	export class TimeBoundsDTO {
	    single_min_unix: number;
	    single_max_exclusive_unix: number;
	    query_max_exclusive_unix: number;

	    static createFrom(source: any = {}) {
	        return new TimeBoundsDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.single_min_unix = source["single_min_unix"];
	        this.single_max_exclusive_unix = source["single_max_exclusive_unix"];
	        this.query_max_exclusive_unix = source["query_max_exclusive_unix"];
	    }
	}

}

export namespace runtime {

	export class ActivationTargetDTO {
	    schedule_id: number;
	    original_date: string;

	    static createFrom(source: any = {}) {
	        return new ActivationTargetDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.schedule_id = source["schedule_id"];
	        this.original_date = source["original_date"];
	    }
	}
	export class ActivationDTO {
	    sequence: number;
	    kind: string;
	    target?: ActivationTargetDTO;

	    static createFrom(source: any = {}) {
	        return new ActivationDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.sequence = source["sequence"];
	        this.kind = source["kind"];
	        this.target = this.convertValues(source["target"], ActivationTargetDTO);
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

	export class CloseBehaviorDTO {
	    behavior: string;

	    static createFrom(source: any = {}) {
	        return new CloseBehaviorDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.behavior = source["behavior"];
	    }
	}
	export class DeleteScheduleDTO {
	    title: string;

	    static createFrom(source: any = {}) {
	        return new DeleteScheduleDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.title = source["title"];
	    }
	}
	export class DesktopStatusDTO {
	    state: string;
	    cleanup_error: string;
	    last_error: string;
	    activation_sequence: number;
	    frontend_ready: boolean;

	    static createFrom(source: any = {}) {
	        return new DesktopStatusDTO(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.cleanup_error = source["cleanup_error"];
	        this.last_error = source["last_error"];
	        this.activation_sequence = source["activation_sequence"];
	        this.frontend_ready = source["frontend_ready"];
	    }
	}
	export class FrontendReadyRequest {
	    last_sequence: number;

	    static createFrom(source: any = {}) {
	        return new FrontendReadyRequest(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.last_sequence = source["last_sequence"];
	    }
	}
	export class FrontendReadyResponse {
	    activation?: ActivationDTO;

	    static createFrom(source: any = {}) {
	        return new FrontendReadyResponse(source);
	    }

	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.activation = this.convertValues(source["activation"], ActivationDTO);
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

}
