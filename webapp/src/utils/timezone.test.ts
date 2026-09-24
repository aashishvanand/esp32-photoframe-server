import { describe, it, expect } from 'vitest';
import { TIMEZONES } from '../data/timezones';
import {
  BROWSER_ZONE_ALIASES,
  DEFAULT_TZ_RULE,
  TIMEZONE_NAMES,
  TZ_RULE_MAX_BYTES,
  ZONE_CAVEATS,
  browserTimeZone,
  choiceForRule,
  fixedOffsetLabel,
  ruleForZone,
  tableZoneName,
  tzRuleProblem,
  zoneCaveat,
  zonesForRule,
} from './timezone';

const CET = 'CET-1CEST,M3.5.0,M10.5.0/3';

describe('the zone table', () => {
  it('lists every zone once, in table order', () => {
    expect(TIMEZONE_NAMES.length).toBe(464);
    expect(new Set(TIMEZONE_NAMES).size).toBe(TIMEZONE_NAMES.length);
    expect(TIMEZONE_NAMES[0]).toBe('Africa/Abidjan');
  });

  it('holds only rules the frame can store', () => {
    for (const name of TIMEZONE_NAMES) {
      expect(tzRuleProblem(TIMEZONES[name]), name).toBeNull();
    }
  });

  it('holds no transition time newlib cannot read', () => {
    // tzset() takes the time after "/" with sscanf("%hu"), so it must carry
    // no sign. Hours past 24 are only added on (Asia/Gaza's M3.4.4/50 is the
    // Saturday after the fourth Thursday), so those are fine.
    for (const name of TIMEZONE_NAMES) {
      const rule = TIMEZONES[name];
      for (const [, time] of rule.matchAll(/\/([^,]*)/g)) {
        expect(time, `${name}: ${rule}`).toMatch(/^\d{1,3}(:[0-5]\d){0,2}$/);
      }
    }
    expect(TIMEZONES['America/Nuuk']).toBe('<-02>2<-01>,M3.5.0/0,M10.5.0/0');
  });

  it("knows every zone this runtime's Intl can report", () => {
    // A browser reports a name from this list (or an older spelling, which
    // the aliases cover); a name missing here disables "Use this browser's
    // time zone". A newer ICU may add a zone: add it to the table.
    // (supportedValuesOf is ES2022; the tsconfig lib stops at ES2020.)
    const intl = Intl as unknown as {
      supportedValuesOf(key: 'timeZone'): string[];
    };
    for (const name of intl.supportedValuesOf('timeZone')) {
      expect(ruleForZone(tableZoneName(name)), name).not.toBeNull();
    }
  });

  it('yields the default rule for UTC', () => {
    expect(ruleForZone('Etc/UTC')).toBe(DEFAULT_TZ_RULE);
  });

  it('carries the 2026 changes upstream posix_tz_db lacks', () => {
    // tzdata 2026b-2026d: no more clock changes in British Columbia,
    // Alberta, the Northwest Territories and Morocco.
    expect(ruleForZone('America/Vancouver')).toBe('MST7');
    expect(ruleForZone('America/Edmonton')).toBe('CST6');
    expect(ruleForZone('America/Yellowknife')).toBe('CST6');
    expect(ruleForZone('America/Inuvik')).toBe('CST6');
    expect(ruleForZone('Africa/Casablanca')).toBe('<+00>0');
    expect(ruleForZone('Africa/El_Aaiun')).toBe('<+00>0');
  });

  it('names a caveat only for zones it has', () => {
    for (const name of Object.keys(ZONE_CAVEATS)) {
      expect(ruleForZone(name), name).not.toBeNull();
    }
    expect(zoneCaveat('America/Nuuk')).toMatch(/one hour late/);
    expect(zoneCaveat('Asia/Gaza')).toMatch(/prediction/);
    expect(zoneCaveat('Europe/Berlin')).toBeNull();
    expect(zoneCaveat('constructor')).toBeNull();
  });
});

describe('ruleForZone', () => {
  it('returns the rule for a known zone', () => {
    expect(ruleForZone('Europe/Berlin')).toBe(CET);
    expect(ruleForZone('Asia/Taipei')).toBe('CST-8');
    expect(ruleForZone('America/New_York')).toBe('EST5EDT,M3.2.0,M11.1.0');
  });

  it('returns null for an unknown zone, including inherited names', () => {
    expect(ruleForZone('Mars/Olympus_Mons')).toBeNull();
    expect(ruleForZone('constructor')).toBeNull();
    expect(ruleForZone('')).toBeNull();
  });
});

describe('zonesForRule', () => {
  it('lists every zone with that rule, in table order', () => {
    const names = zonesForRule(CET);
    expect(names[0]).toBe('Africa/Ceuta');
    expect(names).toContain('Europe/Berlin');
    expect(names).toContain('Europe/Paris');
    for (const name of names) expect(TIMEZONES[name]).toBe(CET);
  });

  it('is empty for a rule no zone yields', () => {
    expect(zonesForRule('UTC-8')).toEqual([]);
    expect(zonesForRule('')).toEqual([]);
  });
});

describe('fixedOffsetLabel', () => {
  it('inverts the POSIX sign for people', () => {
    expect(fixedOffsetLabel('UTC-8')).toBe('Fixed offset UTC+8');
    expect(fixedOffsetLabel('UTC+5')).toBe('Fixed offset UTC-5');
    expect(fixedOffsetLabel('UTC8')).toBe('Fixed offset UTC-8');
  });

  it('keeps minutes when there are any', () => {
    expect(fixedOffsetLabel('UTC-5:30')).toBe('Fixed offset UTC+5:30');
    expect(fixedOffsetLabel('UTC+9:30')).toBe('Fixed offset UTC-9:30');
    expect(fixedOffsetLabel('UTC-8:00')).toBe('Fixed offset UTC+8');
  });

  it('shows zero as +0 whichever way it is written', () => {
    expect(fixedOffsetLabel('UTC0')).toBe('Fixed offset UTC+0');
    expect(fixedOffsetLabel('UTC+0')).toBe('Fixed offset UTC+0');
    expect(fixedOffsetLabel('UTC-0')).toBe('Fixed offset UTC+0');
    expect(fixedOffsetLabel('UTC-00:00')).toBe('Fixed offset UTC+0');
  });

  it('is null for anything but exactly UTC±H[:MM]', () => {
    expect(fixedOffsetLabel('')).toBeNull();
    expect(fixedOffsetLabel('EST5EDT,M3.2.0,M11.1.0')).toBeNull();
    expect(fixedOffsetLabel('UTC-8EDT')).toBeNull();
    expect(fixedOffsetLabel('CST-8')).toBeNull();
    expect(fixedOffsetLabel('UTC-8:0')).toBeNull();
    expect(fixedOffsetLabel('UTC-8:00:00')).toBeNull();
    expect(fixedOffsetLabel(' UTC-8')).toBeNull();
    expect(fixedOffsetLabel('utc-8')).toBeNull();
  });
});

describe('choiceForRule', () => {
  it('prefers a preferred zone when it yields the rule', () => {
    expect(choiceForRule(CET, ['Europe/Berlin'])).toEqual({
      kind: 'zone',
      name: 'Europe/Berlin',
    });
  });

  it('takes the preferred names in order, skipping those that do not fit', () => {
    // The zone the user picked stays on show ahead of the browser's zone...
    expect(choiceForRule(CET, ['Europe/Berlin', 'Europe/Paris'])).toEqual({
      kind: 'zone',
      name: 'Europe/Berlin',
    });
    // ...but once the rule is something else, it no longer applies.
    expect(choiceForRule(CET, ['Asia/Taipei', 'Europe/Paris'])).toEqual({
      kind: 'zone',
      name: 'Europe/Paris',
    });
    expect(choiceForRule(CET, [null, 'Europe/Paris'])).toEqual({
      kind: 'zone',
      name: 'Europe/Paris',
    });
  });

  it('falls back to the first zone with that rule', () => {
    expect(choiceForRule(CET, ['Asia/Taipei'])).toEqual({
      kind: 'zone',
      name: 'Africa/Ceuta',
    });
    expect(choiceForRule(CET, [])).toEqual({
      kind: 'zone',
      name: 'Africa/Ceuta',
    });
    expect(choiceForRule(CET, [null, null])).toEqual({
      kind: 'zone',
      name: 'Africa/Ceuta',
    });
    expect(choiceForRule(CET, ['Mars/Olympus_Mons'])).toEqual({
      kind: 'zone',
      name: 'Africa/Ceuta',
    });
  });

  it('names the default rule', () => {
    const choice = choiceForRule(DEFAULT_TZ_RULE, []);
    expect(choice.kind).toBe('zone');
    if (choice.kind === 'zone') {
      expect(ruleForZone(choice.name)).toBe(DEFAULT_TZ_RULE);
    }
  });

  it('synthesizes a fixed-offset entry for what the old field wrote', () => {
    expect(choiceForRule('UTC-8', [])).toEqual({
      kind: 'fixed',
      label: 'Fixed offset UTC+8',
    });
    expect(choiceForRule('UTC+5:30', ['Asia/Kolkata'])).toEqual({
      kind: 'fixed',
      label: 'Fixed offset UTC-5:30',
    });
  });

  it('is custom for any other rule, including an empty one', () => {
    expect(choiceForRule('EST5EDT,M3.2.0/2,M11.1.0/2', [])).toEqual({
      kind: 'custom',
    });
    expect(choiceForRule('', [])).toEqual({ kind: 'custom' });
    expect(choiceForRule('', ['Europe/Berlin'])).toEqual({ kind: 'custom' });
  });
});

describe('tzRuleProblem', () => {
  it('accepts rules the frame stores', () => {
    expect(tzRuleProblem(DEFAULT_TZ_RULE)).toBeNull();
    expect(tzRuleProblem('UTC-8')).toBeNull();
    expect(tzRuleProblem(CET)).toBeNull();
    expect(tzRuleProblem('<+01>-1')).toBeNull();
    expect(tzRuleProblem('EST5EDT,M3.2.0,M11.1.0')).toBeNull();
    expect(tzRuleProblem('x'.repeat(TZ_RULE_MAX_BYTES))).toBeNull();
  });

  it('refuses an empty rule', () => {
    expect(tzRuleProblem('')).toMatch(/Enter a POSIX TZ rule/);
  });

  it('refuses a rule the frame would truncate', () => {
    expect(tzRuleProblem('x'.repeat(TZ_RULE_MAX_BYTES + 1))).toMatch(
      /63 bytes/
    );
  });

  it('refuses spaces, control characters and non-ASCII', () => {
    expect(tzRuleProblem('UTC-8 ')).toMatch(/printable ASCII/);
    expect(tzRuleProblem('UTC -8')).toMatch(/printable ASCII/);
    expect(tzRuleProblem('UTC-8\n')).toMatch(/printable ASCII/);
    // A Unicode minus sign, as a word processor might write it.
    expect(tzRuleProblem('UTC−8')).toMatch(/printable ASCII/);
    expect(tzRuleProblem('UTC-8\u0000')).toMatch(/printable ASCII/);
  });

  it('reports non-ASCII before length, since bytes exceed characters', () => {
    expect(tzRuleProblem('é'.repeat(40))).toMatch(/printable ASCII/);
  });
});

describe('tableZoneName', () => {
  it('maps what a browser may report to the name the table uses', () => {
    expect(tableZoneName('Asia/Calcutta')).toBe('Asia/Kolkata');
    expect(tableZoneName('Europe/Kyiv')).toBe('Europe/Kiev');
    expect(tableZoneName('UTC')).toBe('Etc/UTC');
  });

  it('leaves table names and unknown names alone', () => {
    expect(tableZoneName('Asia/Kolkata')).toBe('Asia/Kolkata');
    expect(tableZoneName('Etc/Unknown')).toBe('Etc/Unknown');
    expect(tableZoneName('constructor')).toBe('constructor');
  });

  it('aliases only names the table lacks, to names it has', () => {
    for (const [alias, name] of Object.entries(BROWSER_ZONE_ALIASES)) {
      expect(ruleForZone(alias), alias).toBeNull();
      expect(ruleForZone(name), name).not.toBeNull();
    }
  });
});

describe('browserTimeZone', () => {
  it('returns a table name, an unknown name, or null', () => {
    const zone = browserTimeZone();
    expect(zone === null || typeof zone === 'string').toBe(true);
    if (zone !== null) {
      expect(zone).not.toBe('');
      expect(tableZoneName(zone)).toBe(zone);
    }
  });
});
