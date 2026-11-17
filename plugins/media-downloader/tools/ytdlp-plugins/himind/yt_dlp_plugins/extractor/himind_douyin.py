"""Parse private Douyin detail using the pinned yt-dlp's built-in parser.

No credentials are copied to returned metadata or media request headers.
"""
import json
import os

from yt_dlp.extractor.tiktok import DouyinIE
from yt_dlp.utils import ExtractorError


class _HiMindDouyinIE(DouyinIE, plugin_name='HiMind'):
    def _real_extract(self, url):
        video_id = self._match_id(url)
        try:
            with open(os.environ['HIMIND_DOUYIN_DETAIL_FILE'], encoding='utf-8') as source:
                detail = json.load(source)
        except (OSError, KeyError, ValueError):
            raise ExtractorError('Douyin detail unavailable', expected=True)
        if (not isinstance(detail, dict) or str(detail.get('aweme_id')) != video_id
                or not isinstance(detail.get('video'), dict)):
            raise ExtractorError('Douyin detail mismatch', expected=True)
        return self._parse_aweme_video_app(detail)
