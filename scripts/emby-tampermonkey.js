// ==UserScript==
// @name         Emby mpv Middleware
// @namespace    https://github.com/xmengnet/simple-emby
// @version      2.0
// @description  Emby 调用本地 mpv 播放器
// @author       xmengnet
// @match        *://*/web/index.html*
// @match        *://*/*/web/index.html*
// @match        *://*/web/
// @match        *://*/*/web/
// @icon         https://www.google.com/s2/favicons?sz=64&domain=emby.media
// @grant        unsafeWindow
// @grant        GM_xmlhttpRequest
// @connect      127.0.0.1
// @run-at       document-start
// @license      MIT
// ==/UserScript==
'use strict';
/*global ApiClient*/

(function () {
    'use strict';

    const originFetch = fetch;
    const LOCAL_SERVER = 'http://127.0.0.1:19999';

    function logger(...args) {
        console.log('%c[emby-mpv]', 'color: #0f0; font-weight: bold;', ...args);
    }

    async function sleep(ms) {
        return new Promise(resolve => setTimeout(resolve, ms));
    }

    // 自动关闭 Emby 弹出的播放错误窗口
    function removeErrorWindows() {
        let okButtonList = document.querySelectorAll('button[data-id="ok"]');
        let state = false;
        for (let i = 0; i < okButtonList.length; i++) {
            const el = okButtonList[i];
            if (el.textContent.search(/.+/) != -1 && el.offsetParent !== null) {
                el.click();
                state = true;
            }
        }
        return state;
    }

    async function removeErrorWindowsLoop() {
        for (let i = 0; i < 15; i++) {
            await sleep(200);
            if (removeErrorWindows()) {
                logger('已自动关闭错误弹窗');
                break;
            }
        }
    }

    function sendToLocalServer(data) {
        let url = `${LOCAL_SERVER}/play`;
        GM_xmlhttpRequest({
            method: 'POST',
            url: url,
            data: JSON.stringify(data),
            headers: {
                'Content-Type': 'application/json'
            },
            onload: function (response) {
                logger('mpv 播放请求已发送:', response.responseText);
            },
            onerror: function (error) {
                alert(`${url}\n请求错误，本地 simple-emby 服务未运行。`);
                console.error('请求错误:', error);
            }
        });
        logger('sendToLocalServer', data);
    }

    async function dealWithPlaybackInfo(urlStr, input, options) {
        // 从 URL 中提取 ItemId
        let itemId = urlStr.match(/\/Items\/(\w+)\/PlaybackInfo/)?.[1];
        if (!itemId) return false;

        // 获取认证信息 - 直接从 ApiClient 拿，最可靠
        let serverUrl, apiKey, userId;
        try {
            serverUrl = ApiClient.serverAddress();
            apiKey = ApiClient.accessToken();
            userId = ApiClient.getCurrentUserId();
        } catch (e) {
            logger('无法从 ApiClient 获取认证信息:', e);
            return false;
        }

        if (!serverUrl || !apiKey || !userId) {
            logger('认证信息不完整:', { serverUrl, apiKey, userId });
            return false;
        }

        // 获取媒体标题
        let mediaTitle = '';
        try {
            let itemInfo = await ApiClient.getItem(userId, itemId);
            if (itemInfo) {
                if (itemInfo.SeriesName) {
                    mediaTitle = `${itemInfo.SeriesName} - S${String(itemInfo.ParentIndexNumber || 0).padStart(2, '0')}E${String(itemInfo.IndexNumber || 0).padStart(2, '0')}`;
                    if (itemInfo.Name) {
                        mediaTitle += ` - ${itemInfo.Name}`;
                    }
                } else {
                    mediaTitle = itemInfo.Name || '';
                }
            }
        } catch (e) {
            logger('获取标题失败，使用页面标题:', e);
            mediaTitle = document.title.replace(/ - Emby$/, '').trim();
        }

        logger(`拦截播放: ItemId=${itemId}, Title=${mediaTitle}`);

        sendToLocalServer({
            server_url: serverUrl,
            api_key: apiKey,
            user_id: userId,
            item_id: itemId,
            media_title: mediaTitle
        });

        removeErrorWindowsLoop();
        return true;
    }

    // 核心：覆盖 unsafeWindow.fetch 拦截 Emby 的播放请求
    unsafeWindow.fetch = async (input, options) => {
        let isStrInput = typeof input === 'string';
        let urlStr = isStrInput ? input : input.url;

        try {
            // 只拦截真正的播放请求 (包含 IsPlayback=true)
            if (urlStr.indexOf('/PlaybackInfo?') != -1 || urlStr.indexOf('/PlaybackInfo%3F') != -1) {
                if (urlStr.indexOf('IsPlayback=true') != -1) {
                    logger('检测到播放请求:', urlStr);
                    let dealt = await dealWithPlaybackInfo(urlStr, input, options);
                    if (dealt) {
                        // 返回空，阻止网页播放器启动
                        return;
                    }
                }
            }
        } catch (error) {
            console.error('[emby-mpv] 拦截出错:', error);
        }

        return originFetch(input, options);
    };

    // 同样处理 XMLHttpRequest (Jellyfin 兼容)
    function initXMLHttpRequest() {
        const originOpen = XMLHttpRequest.prototype.open;
        const originSend = XMLHttpRequest.prototype.send;
        const originSetHeader = XMLHttpRequest.prototype.setRequestHeader;

        XMLHttpRequest.prototype.setRequestHeader = function (header, value) {
            if (!this._headers) this._headers = {};
            this._headers[header] = value;
            return originSetHeader.apply(this, arguments);
        };

        XMLHttpRequest.prototype.open = function (method, url) {
            this._method = method;
            this._url = url;
            this._headers = {};
            return originOpen.apply(this, arguments);
        };

        XMLHttpRequest.prototype.send = function (body) {
            // Jellyfin 用 POST 发 PlaybackInfo
            if (this._method === 'POST' && this._url && this._url.endsWith('PlaybackInfo')) {
                logger('XHR PlaybackInfo (Jellyfin) detected:', this._url);
                // For Jellyfin, handle similarly
            }
            return originSend.apply(this, arguments);
        };
    }

    initXMLHttpRequest();

    logger('脚本已加载，等待播放请求...');

})();
