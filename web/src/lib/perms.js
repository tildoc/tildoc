import { extOf } from './format.js';
import { labels } from './i18n/index.js';

// Names for the permission catalogue (internal/core/permissions.go).
// a PDF can go to the office server if the user may change it there
export const canEditPdfInOffice = (doc, name, office, spaces) =>
  (doc?.mime ?? '').startsWith('application/pdf') && doc?.size > 0 && (office?.editable ?? []).includes(extOf(name))
  && spaces?.find((s) => s.id === doc.space_id)?.perm >= 2;
export const canShareAny = (caps) => (caps ?? []).some((c) => c.startsWith('share.'));
// a document without a permission list (public links) decides by the link
export const canDownload = (doc) => !doc?.caps || doc.caps.includes('download');

const PERM_LABEL = labels({ view: 'perm_view', download: 'perm_download', export: 'perm_export', write: 'perm_write', move: 'perm_move', delete: 'perm_delete', purge: 'perm_purge', manage: 'perm_manage', transfer: 'perm_transfer', drop_in: 'perm_drop_in', "share.space.people": 'perm_share_space_people', "share.space.login_link": 'perm_share_space_login_link', "share.space.public_link": 'perm_share_space_public_link', "share.space.drop_link": 'perm_share_space_drop_link', "share.file.people": 'perm_share_file_people', "share.file.login_link": 'perm_share_file_login_link', "share.file.public_link": 'perm_share_file_public_link', "users.manage": 'perm_users_manage', "teams.manage": 'perm_teams_manage', "teams.create": 'perm_teams_create', "roles.manage": 'perm_roles_manage', "spaces.create": 'perm_spaces_create', "spaces.recover": 'perm_spaces_recover', "tags.manage": 'perm_tags_manage', "backups.manage": 'perm_backups_manage', "settings.manage": 'perm_settings_manage', "audit.view": 'perm_audit_view' });
const PERM_HELP = labels({ view: 'permhelp_view', download: 'permhelp_download', export: 'permhelp_export', write: 'permhelp_write', move: 'permhelp_move', delete: 'permhelp_delete', purge: 'permhelp_purge', manage: 'permhelp_manage', transfer: 'permhelp_transfer', drop_in: 'permhelp_drop_in', "share.space.people": 'permhelp_share_space_people', "share.space.login_link": 'permhelp_share_space_login_link', "share.space.public_link": 'permhelp_share_space_public_link', "share.space.drop_link": 'permhelp_share_space_drop_link', "share.file.people": 'permhelp_share_file_people', "share.file.login_link": 'permhelp_share_file_login_link', "share.file.public_link": 'permhelp_share_file_public_link', "users.manage": 'permhelp_users_manage', "teams.manage": 'permhelp_teams_manage', "teams.create": 'permhelp_teams_create', "roles.manage": 'permhelp_roles_manage', "spaces.create": 'permhelp_spaces_create', "spaces.recover": 'permhelp_spaces_recover', "tags.manage": 'permhelp_tags_manage', "backups.manage": 'permhelp_backups_manage', "settings.manage": 'permhelp_settings_manage', "audit.view": 'permhelp_audit_view' });
export const permLabel = (p) => PERM_LABEL[p] ?? p;
export const permHelp = (p) => PERM_HELP[p] ?? '';
