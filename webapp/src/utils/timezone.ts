import { TIMEZONES } from '../data/timezones';

// The frame applies its `timezone` config with setenv("TZ")/tzset(), so it
// takes any POSIX TZ rule, DST transitions included. These helpers name a rule
// for the picker and check the little the firmware would mishandle silently.

// The firmware keeps the rule in a 64-byte buffer (TIMEZONE_MAX_LEN) and
// strncpy()s into it, so anything past 63 bytes is cut off without a word.
export const TZ_RULE_MAX_BYTES = 63;

// The firmware's own default: plain UTC.
export const DEFAULT_TZ_RULE = 'UTC0';

// IANA names, in table order (alphabetical); the picker lists them as-is.
export const TIMEZONE_NAMES: string[] = Object.keys(TIMEZONES);

// What the picker shows for a rule: a zone that yields it, a fixed offset in
// the form the old numeric field wrote (UTC±H[:MM]), or a rule it cannot name.
export type ZoneChoice =
  | { kind: 'zone'; name: string }
  | { kind: 'fixed'; label: string }
  | { kind: 'custom' };

export function ruleForZone(name: string): string | null {
  return Object.prototype.hasOwnProperty.call(TIMEZONES, name)
    ? TIMEZONES[name]
    : null;
}

// zonesForRule lists every zone whose rule is exactly `rule`, in table order.
export function zonesForRule(rule: string): string[] {
  return TIMEZONE_NAMES.filter((name) => TIMEZONES[name] === rule);
}

const GREENLAND_CAVEAT =
  'On the frame, summer time here starts one hour late (Sunday 00:00 ' +
  'rather than Saturday 23:00): its tzset() cannot read the exact rule.';
const PALESTINE_CAVEAT =
  'Palestine announces its clock changes year by year; this rule is the ' +
  "tz database's prediction and may miss a change.";

// Zones whose rule the frame will not follow exactly, with how far off it
// gets; the picker shows this under the zone. Everything else in the table
// is what tzdata itself predicts for the years ahead.
export const ZONE_CAVEATS: Record<string, string> = {
  'America/Godthab': GREENLAND_CAVEAT,
  'America/Nuuk': GREENLAND_CAVEAT,
  'America/Scoresbysund': GREENLAND_CAVEAT,
  'Asia/Gaza': PALESTINE_CAVEAT,
  'Asia/Hebron': PALESTINE_CAVEAT,
};

export function zoneCaveat(name: string): string | null {
  return Object.prototype.hasOwnProperty.call(ZONE_CAVEATS, name)
    ? ZONE_CAVEATS[name]
    : null;
}

// Zone names a browser may report for a zone the table lists under another
// name. ICU-based engines (Chrome, Safari, Node) long reported the CLDR
// spelling, which is the IANA name a zone had before it was renamed
// (Asia/Calcutta for Asia/Kolkata); the table has two zones the other way
// round, still under their old name. Only names the table lacks belong here.
export const BROWSER_ZONE_ALIASES: Record<string, string> = {
  'Africa/Asmera': 'Africa/Asmara',
  'America/Buenos_Aires': 'America/Argentina/Buenos_Aires',
  'America/Catamarca': 'America/Argentina/Catamarca',
  'America/Coral_Harbour': 'America/Atikokan',
  'America/Cordoba': 'America/Argentina/Cordoba',
  'America/Indianapolis': 'America/Indiana/Indianapolis',
  'America/Jujuy': 'America/Argentina/Jujuy',
  'America/Louisville': 'America/Kentucky/Louisville',
  'America/Mendoza': 'America/Argentina/Mendoza',
  'Asia/Ashkhabad': 'Asia/Ashgabat',
  'Asia/Calcutta': 'Asia/Kolkata',
  'Asia/Chongqing': 'Asia/Shanghai',
  'Asia/Dacca': 'Asia/Dhaka',
  'Asia/Kashgar': 'Asia/Urumqi',
  'Asia/Katmandu': 'Asia/Kathmandu',
  'Asia/Macao': 'Asia/Macau',
  'Asia/Rangoon': 'Asia/Yangon',
  'Asia/Saigon': 'Asia/Ho_Chi_Minh',
  'Asia/Thimbu': 'Asia/Thimphu',
  'Asia/Ujung_Pandang': 'Asia/Makassar',
  'Asia/Ulan_Bator': 'Asia/Ulaanbaatar',
  'Atlantic/Faeroe': 'Atlantic/Faroe',
  'Atlantic/Jan_Mayen': 'Europe/Berlin',
  'Europe/Kyiv': 'Europe/Kiev',
  GMT: 'Etc/GMT',
  'Pacific/Kanton': 'Pacific/Enderbury',
  'Pacific/Ponape': 'Pacific/Pohnpei',
  'Pacific/Truk': 'Pacific/Chuuk',
  UTC: 'Etc/UTC',
};

// tableZoneName returns the name the table uses for a zone a browser
// reported: the name itself, or the table's spelling of an alias. A name the
// table does not know at all comes back unchanged.
export function tableZoneName(name: string): string {
  return Object.prototype.hasOwnProperty.call(BROWSER_ZONE_ALIASES, name)
    ? BROWSER_ZONE_ALIASES[name]
    : name;
}

// browserTimeZone returns this browser's zone as the table names it, or null
// when the runtime does not say.
export function browserTimeZone(): string | null {
  let name: string | undefined;
  try {
    name = Intl.DateTimeFormat().resolvedOptions().timeZone;
  } catch {
    return null;
  }
  return name ? tableZoneName(name) : null;
}

// fixedOffsetLabel turns a rule that is exactly UTC±H[:MM] into a label with
// the sign people expect, or returns null for anything else. POSIX counts
// hours west of Greenwich, so the frame's "UTC-8" is UTC+8 to a person.
export function fixedOffsetLabel(rule: string): string | null {
  const m = /^UTC([+-]?)(\d{1,2})(?::(\d{2}))?$/.exec(rule);
  if (!m) return null;
  const hours = Number(m[2]);
  const minutes = m[3] === undefined ? 0 : Number(m[3]);
  const east = m[1] === '-' || (hours === 0 && minutes === 0);
  const hhmm =
    minutes === 0 ? `${hours}` : `${hours}:${String(minutes).padStart(2, '0')}`;
  return `Fixed offset UTC${east ? '+' : '-'}${hhmm}`;
}

// choiceForRule picks what the picker shows for `rule`: the first name in
// `preferred` (the one the user picked, the browser's zone) that yields the
// rule, else the first zone in the table that does, else a fixed-offset
// label, else "custom". Many zones share a rule, so without a preference
// Europe/Berlin would show as Africa/Ceuta.
export function choiceForRule(
  rule: string,
  preferred: ReadonlyArray<string | null>
): ZoneChoice {
  for (const name of preferred) {
    if (name !== null && ruleForZone(name) === rule) {
      return { kind: 'zone', name };
    }
  }
  const names = zonesForRule(rule);
  if (names.length > 0) {
    return { kind: 'zone', name: names[0] };
  }
  const label = fixedOffsetLabel(rule);
  if (label !== null) {
    return { kind: 'fixed', label };
  }
  return { kind: 'custom' };
}

// tzRuleProblem says why a rule cannot be sent to the frame, or null when it
// can. Only what the frame would take silently is refused: an empty rule, a
// rule it would truncate, and characters outside the POSIX TZ grammar (which
// is printable ASCII with no spaces). The grammar itself is not parsed here;
// a well-formed-looking typo still reaches tzset().
export function tzRuleProblem(rule: string): string | null {
  if (rule === '') {
    return 'Enter a POSIX TZ rule, or pick a time zone from the list.';
  }
  if (!/^[\x21-\x7e]+$/.test(rule)) {
    return 'A POSIX TZ rule is printable ASCII with no spaces.';
  }
  // Every character passed the ASCII check, so length is bytes.
  if (rule.length > TZ_RULE_MAX_BYTES) {
    return `The frame keeps at most ${TZ_RULE_MAX_BYTES} bytes of rule.`;
  }
  return null;
}
